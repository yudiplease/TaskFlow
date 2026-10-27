package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tasktracker/internal/updatechecker"
)

// Updates — репозиторий кэша релизов и состояния авто-проверки обновлений
// на PostgreSQL. Обе таблицы single-row (CHECK (id = 1)).
type Updates struct{ pool *pgxpool.Pool }

// NewUpdates создаёт репозиторий.
func NewUpdates(pool *pgxpool.Pool) *Updates { return &Updates{pool} }

// GetReleaseCache возвращает кэшированный релиз и ETag; ErrNotFound, если кэша нет.
func (r *Updates) GetReleaseCache(ctx context.Context) (*updatechecker.Release, string, error) {
	var (
		rel         updatechecker.Release
		publishedAt *time.Time
		etag        string
	)
	err := r.pool.QueryRow(ctx, `
		SELECT tag_name, release_name, html_url, published_at, etag, checked_at
		FROM release_cache WHERE id = 1`,
	).Scan(&rel.Tag, &rel.Name, &rel.URL, &publishedAt, &etag, &rel.CheckedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", updatechecker.ErrNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("get release cache: %w", err)
	}
	if publishedAt != nil {
		rel.PublishedAt = *publishedAt
	}
	return &rel, etag, nil
}

// UpsertReleaseCache сохраняет релиз атомарной транзакцией: сам кэш +
// отметка времени проверки в update_state (и сброс next_check_after).
func (r *Updates) UpsertReleaseCache(ctx context.Context, rel *updatechecker.Release, etag string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("upsert release cache: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var publishedAt any
	if !rel.PublishedAt.IsZero() {
		publishedAt = rel.PublishedAt
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO release_cache (id, tag_name, release_name, html_url, published_at, etag, checked_at)
		VALUES (1, $1, $2, $3, $4, $5, $6)
		ON CONFLICT(id) DO UPDATE SET
			tag_name = $1, release_name = $2, html_url = $3,
			published_at = $4, etag = $5, checked_at = $6`,
		rel.Tag, rel.Name, rel.URL, publishedAt, etag, rel.CheckedAt,
	); err != nil {
		return fmt.Errorf("upsert release cache: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO update_state (id, enabled, interval_hours, last_checked_at, next_check_after)
		VALUES (1, TRUE, 24, $1, NULL)
		ON CONFLICT(id) DO UPDATE SET last_checked_at = $1, next_check_after = NULL`,
		rel.CheckedAt,
	); err != nil {
		return fmt.Errorf("upsert release cache (state): %w", err)
	}
	return tx.Commit(ctx)
}

// TouchReleaseCache — путь 304 Not Modified: обновляем checked_at
// и last_checked_at, содержимое кэша не трогаем.
func (r *Updates) TouchReleaseCache(ctx context.Context, checkedAt time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("touch release cache: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `UPDATE release_cache SET checked_at = $1 WHERE id = 1`, checkedAt)
	if err != nil {
		return fmt.Errorf("touch release cache: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return updatechecker.ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO update_state (id, enabled, interval_hours, last_checked_at, next_check_after)
		VALUES (1, TRUE, 24, $1, NULL)
		ON CONFLICT(id) DO UPDATE SET last_checked_at = $1, next_check_after = NULL`,
		checkedAt,
	); err != nil {
		return fmt.Errorf("touch release cache (state): %w", err)
	}
	return tx.Commit(ctx)
}

// GetUpdateState возвращает настройки проверки; ErrNotFound, если строки ещё нет.
func (r *Updates) GetUpdateState(ctx context.Context) (*updatechecker.UpdateState, error) {
	var (
		st             updatechecker.UpdateState
		enabled        bool
		lastChecked    sql.NullTime
		nextCheckAfter sql.NullTime
	)
	err := r.pool.QueryRow(ctx, `
		SELECT enabled, interval_hours, last_checked_at, next_check_after
		FROM update_state WHERE id = 1`,
	).Scan(&enabled, &st.IntervalHours, &lastChecked, &nextCheckAfter)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, updatechecker.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get update state: %w", err)
	}
	st.Enabled = enabled
	if lastChecked.Valid {
		t := lastChecked.Time
		st.LastCheckedAt = &t
	}
	if nextCheckAfter.Valid {
		t := nextCheckAfter.Time
		st.NextCheckAfter = &t
	}
	return &st, nil
}

// UpsertUpdateState сохраняет состояние целиком (upsert single-row).
func (r *Updates) UpsertUpdateState(ctx context.Context, st *updatechecker.UpdateState) error {
	var lastChecked, nextCheckAfter any
	if st.LastCheckedAt != nil {
		lastChecked = *st.LastCheckedAt
	}
	if st.NextCheckAfter != nil {
		nextCheckAfter = *st.NextCheckAfter
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO update_state (id, enabled, interval_hours, last_checked_at, next_check_after)
		VALUES (1, $1, $2, $3, $4)
		ON CONFLICT(id) DO UPDATE SET
			enabled = $1, interval_hours = $2, last_checked_at = $3, next_check_after = $4`,
		st.Enabled, st.IntervalHours, lastChecked, nextCheckAfter)
	if err != nil {
		return fmt.Errorf("upsert update state: %w", err)
	}
	return nil
}

var _ updatechecker.Store = (*Updates)(nil)
