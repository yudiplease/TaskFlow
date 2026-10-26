package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"tasktracker/internal/domain"
	"tasktracker/internal/storage"
)

// Activity — репозиторий журнала событий.
type Activity struct {
	pool *pgxpool.Pool
}

// NewActivity создаёт репозиторий журнала.
func NewActivity(pool *pgxpool.Pool) *Activity { return &Activity{pool: pool} }

// Insert добавляет запись о событии.
func (r *Activity) Insert(ctx context.Context, a *domain.Activity) error {
	payload, err := json.Marshal(a.Payload)
	if err != nil {
		return fmt.Errorf("кодирование payload: %w", err)
	}
	taskID := any(nil)
	if a.TaskID != "" {
		taskID = a.TaskID
	}
	return r.pool.QueryRow(ctx, `
		INSERT INTO activity (task_id, type, actor, payload, created_at)
		VALUES ($1::uuid, $2, $3, $4, $5) RETURNING id`,
		taskID, a.Type, a.Actor, payload, a.CreatedAt).Scan(&a.ID)
}

func (r *Activity) query(ctx context.Context, q string, args ...any) ([]domain.Activity, error) {
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("select активности: %w", err)
	}
	defer rows.Close()

	out := []domain.Activity{}
	for rows.Next() {
		var a domain.Activity
		var taskID any
		var payload []byte
		if err := rows.Scan(&a.ID, &taskID, &a.Type, &a.Actor, &payload, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan активности: %w", err)
		}
		if s, ok := taskID.(string); ok {
			a.TaskID = s
		}
		a.Payload = map[string]any{}
		_ = json.Unmarshal(payload, &a.Payload)
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListByTask возвращает события конкретной задачи.
func (r *Activity) ListByTask(ctx context.Context, taskID string, limit int) ([]domain.Activity, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return r.query(ctx, `
		SELECT id, task_id, type, actor, payload, created_at
		FROM activity WHERE task_id = $1::uuid
		ORDER BY id DESC LIMIT $2`, taskID, limit)
}

// ListRecent возвращает последние события по всем задачам.
func (r *Activity) ListRecent(ctx context.Context, limit int) ([]domain.Activity, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return r.query(ctx, `
		SELECT id, task_id, type, actor, payload, created_at
		FROM activity ORDER BY id DESC LIMIT $1`, limit)
}

var _ storage.ActivityRepository = (*Activity)(nil)
