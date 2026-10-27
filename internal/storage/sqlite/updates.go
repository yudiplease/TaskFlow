package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"tasktracker/internal/updatechecker"
)

// Updates — репозиторий кэша релизов и состояния авто-проверки обновлений.
// Обе таблицы single-row (CHECK (id = 1)): состояние ровно одно на приложение.
type Updates struct{ s *Store }

// NewUpdates создаёт репозиторий.
func NewUpdates(s *Store) *Updates { return &Updates{s} }

// GetReleaseCache возвращает кэшированный релиз и ETag; ErrNotFound, если кэша нет.
func (r *Updates) GetReleaseCache(ctx context.Context) (*updatechecker.Release, string, error) {
	var (
		rel         updatechecker.Release
		publishedAt sql.NullString
		etag        string
		checkedAt   string
	)
	err := r.s.db.QueryRowContext(ctx, `
		SELECT tag_name, release_name, html_url, published_at, etag, checked_at
		FROM release_cache WHERE id = 1`,
	).Scan(&rel.Tag, &rel.Name, &rel.URL, &publishedAt, &etag, &checkedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", updatechecker.ErrNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("get release cache: %w", err)
	}
	if pub, err := parseTimePtr(publishedAt); err != nil {
		return nil, "", err
	} else if pub != nil {
		rel.PublishedAt = *pub
	}
	checked, err := parseTime(sql.NullString{String: checkedAt, Valid: true})
	if err != nil {
		return nil, "", err
	}
	rel.CheckedAt = checked
	return &rel, etag, nil
}

// UpsertReleaseCache сохраняет релиз атомарно: сам кэш + отметка времени
// проверки в update_state (и сброс next_check_after — проверка удалась).
// Транзакция защищает от гонок воркера и кнопки «Проверить сейчас».
func (r *Updates) UpsertReleaseCache(ctx context.Context, rel *updatechecker.Release, etag string) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("upsert release cache: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var publishedAt any
	if !rel.PublishedAt.IsZero() {
		publishedAt = formatTime(rel.PublishedAt)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO release_cache (id, tag_name, release_name, html_url, published_at, etag, checked_at)
		VALUES (1, $1, $2, $3, $4, $5, $6)
		ON CONFLICT(id) DO UPDATE SET
			tag_name = $1, release_name = $2, html_url = $3,
			published_at = $4, etag = $5, checked_at = $6`,
		rel.Tag, rel.Name, rel.URL, publishedAt, etag, formatTime(rel.CheckedAt),
	); err != nil {
		return fmt.Errorf("upsert release cache: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO update_state (id, enabled, interval_hours, last_checked_at, next_check_after)
		VALUES (1, 1, 24, $1, NULL)
		ON CONFLICT(id) DO UPDATE SET last_checked_at = $1, next_check_after = NULL`,
		formatTime(rel.CheckedAt),
	); err != nil {
		return fmt.Errorf("upsert release cache (state): %w", err)
	}
	return tx.Commit()
}

// TouchReleaseCache — путь 304 Not Modified: тело не приходило, но проверка
// была; обновляем checked_at (и last_checked_at), содержимое не трогаем.
func (r *Updates) TouchReleaseCache(ctx context.Context, checkedAt time.Time) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("touch release cache: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	tag, err := tx.ExecContext(ctx,
		`UPDATE release_cache SET checked_at = $1 WHERE id = 1`, formatTime(checkedAt))
	if err != nil {
		return fmt.Errorf("touch release cache: %w", err)
	}
	if n, err := tag.RowsAffected(); err == nil && n == 0 {
		return updatechecker.ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO update_state (id, enabled, interval_hours, last_checked_at, next_check_after)
		VALUES (1, 1, 24, $1, NULL)
		ON CONFLICT(id) DO UPDATE SET last_checked_at = $1, next_check_after = NULL`,
		formatTime(checkedAt),
	); err != nil {
		return fmt.Errorf("touch release cache (state): %w", err)
	}
	return tx.Commit()
}

// GetUpdateState возвращает настройки проверки; ErrNotFound, если строки ещё нет.
func (r *Updates) GetUpdateState(ctx context.Context) (*updatechecker.UpdateState, error) {
	var (
		st             updatechecker.UpdateState
		enabled        int
		lastChecked    sql.NullString
		nextCheckAfter sql.NullString
	)
	err := r.s.db.QueryRowContext(ctx, `
		SELECT enabled, interval_hours, last_checked_at, next_check_after
		FROM update_state WHERE id = 1`,
	).Scan(&enabled, &st.IntervalHours, &lastChecked, &nextCheckAfter)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, updatechecker.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get update state: %w", err)
	}
	st.Enabled = enabled != 0
	if st.LastCheckedAt, err = parseTimePtr(lastChecked); err != nil {
		return nil, err
	}
	if st.NextCheckAfter, err = parseTimePtr(nextCheckAfter); err != nil {
		return nil, err
	}
	return &st, nil
}

// UpsertUpdateState сохраняет состояние целиком (upsert single-row).
func (r *Updates) UpsertUpdateState(ctx context.Context, st *updatechecker.UpdateState) error {
	enabled := 0
	if st.Enabled {
		enabled = 1
	}
	var lastChecked, nextCheckAfter any
	if st.LastCheckedAt != nil {
		lastChecked = formatTime(*st.LastCheckedAt)
	}
	if st.NextCheckAfter != nil {
		nextCheckAfter = formatTime(*st.NextCheckAfter)
	}
	_, err := r.s.db.ExecContext(ctx, `
		INSERT INTO update_state (id, enabled, interval_hours, last_checked_at, next_check_after)
		VALUES (1, $1, $2, $3, $4)
		ON CONFLICT(id) DO UPDATE SET
			enabled = $1, interval_hours = $2, last_checked_at = $3, next_check_after = $4`,
		enabled, st.IntervalHours, lastChecked, nextCheckAfter)
	if err != nil {
		return fmt.Errorf("upsert update state: %w", err)
	}
	return nil
}

var _ updatechecker.Store = (*Updates)(nil)
