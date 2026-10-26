package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tasktracker/internal/domain"
	"tasktracker/internal/storage"
)

// Defaults — репозиторий «залипающих» значений формы (per-user).
type Defaults struct{ pool *pgxpool.Pool }

// NewDefaults создаёт репозиторий.
func NewDefaults(pool *pgxpool.Pool) *Defaults { return &Defaults{pool} }

// Get возвращает значения и время обновления.
func (r *Defaults) Get(ctx context.Context, actor string) (domain.TaskDefaults, time.Time, error) {
	var payload []byte
	var updatedAt time.Time
	err := r.pool.QueryRow(ctx,
		`SELECT payload, updated_at FROM user_task_defaults WHERE actor = $1`, actor).
		Scan(&payload, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.TaskDefaults{}, time.Time{}, storage.ErrNotFound
	}
	if err != nil {
		return domain.TaskDefaults{}, time.Time{}, fmt.Errorf("get defaults: %w", err)
	}
	var d domain.TaskDefaults
	if err := json.Unmarshal(payload, &d); err != nil {
		return domain.TaskDefaults{}, time.Time{}, fmt.Errorf("распарсить defaults: %w", err)
	}
	return d, updatedAt.UTC(), nil
}

// Put сохраняет значения (полная перезапись последнего состояния).
func (r *Defaults) Put(ctx context.Context, actor string, d domain.TaskDefaults) (time.Time, error) {
	payload, err := json.Marshal(d)
	if err != nil {
		return time.Time{}, fmt.Errorf("marshal defaults: %w", err)
	}
	now := time.Now().UTC()
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO user_task_defaults (actor, payload, updated_at)
		VALUES ($1, $2, $3)
		ON CONFLICT(actor) DO UPDATE SET payload = $2, updated_at = $3`,
		actor, payload, now)
	if err != nil {
		return time.Time{}, fmt.Errorf("put defaults: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return time.Time{}, storage.ErrNotFound
	}
	return now, nil
}

var _ storage.DefaultsRepository = (*Defaults)(nil)
