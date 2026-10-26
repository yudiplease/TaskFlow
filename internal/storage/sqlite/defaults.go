package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"tasktracker/internal/domain"
	"tasktracker/internal/storage"
)

// Defaults — репозиторий «залипающих» значений формы (per-user).
type Defaults struct{ s *Store }

// NewDefaults создаёт репозиторий.
func NewDefaults(s *Store) *Defaults { return &Defaults{s} }

// Get возвращает значения и время обновления.
func (r *Defaults) Get(ctx context.Context, actor string) (domain.TaskDefaults, time.Time, error) {
	var payload, updatedAt string
	err := r.s.db.QueryRowContext(ctx,
		`SELECT payload, updated_at FROM user_task_defaults WHERE actor = $1`, actor).
		Scan(&payload, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.TaskDefaults{}, time.Time{}, storage.ErrNotFound
	}
	if err != nil {
		return domain.TaskDefaults{}, time.Time{}, fmt.Errorf("get defaults: %w", err)
	}
	var d domain.TaskDefaults
	if err := json.Unmarshal([]byte(payload), &d); err != nil {
		return domain.TaskDefaults{}, time.Time{}, fmt.Errorf("распарсить defaults: %w", err)
	}
	ts, err := timeParseUTC(updatedAt)
	if err != nil {
		return domain.TaskDefaults{}, time.Time{}, err
	}
	return d, ts, nil
}

// Put сохраняет значения (полная перезапись последнего состояния).
func (r *Defaults) Put(ctx context.Context, actor string, d domain.TaskDefaults) (time.Time, error) {
	payload, err := json.Marshal(d)
	if err != nil {
		return time.Time{}, fmt.Errorf("marshal defaults: %w", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	_, err = r.s.db.ExecContext(ctx, `
		INSERT INTO user_task_defaults (actor, payload, updated_at)
		VALUES ($1, $2, $3)
		ON CONFLICT(actor) DO UPDATE SET payload = $2, updated_at = $3`,
		actor, string(payload), formatTime(now))
	if err != nil {
		return time.Time{}, fmt.Errorf("put defaults: %w", err)
	}
	return now, nil
}

var _ storage.DefaultsRepository = (*Defaults)(nil)
