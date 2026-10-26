package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"tasktracker/internal/domain"
	"tasktracker/internal/storage"
)

// Activity — журнал событий в SQLite.
type Activity struct{ s *Store }

// NewActivity создаёт репозиторий журнала.
func NewActivity(s *Store) *Activity { return &Activity{s} }

// Insert добавляет запись о событии.
func (r *Activity) Insert(ctx context.Context, a *domain.Activity) error {
	payload, err := json.Marshal(a.Payload)
	if err != nil {
		return fmt.Errorf("кодирование payload: %w", err)
	}
	var taskID any
	if a.TaskID != "" {
		taskID = a.TaskID
	}
	return r.s.db.QueryRowContext(ctx, `
		INSERT INTO activity (task_id, type, actor, payload, created_at)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		taskID, a.Type, a.Actor, string(payload), formatTime(a.CreatedAt)).Scan(&a.ID)
}

func (r *Activity) query(ctx context.Context, q string, args ...any) ([]domain.Activity, error) {
	rows, err := r.s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("select активности: %w", err)
	}
	defer rows.Close()

	out := []domain.Activity{}
	for rows.Next() {
		var a domain.Activity
		var taskID sql.NullString
		var payload string
		var createdAt string
		if err := rows.Scan(&a.ID, &taskID, &a.Type, &a.Actor, &payload, &createdAt); err != nil {
			return nil, fmt.Errorf("scan активности: %w", err)
		}
		a.TaskID = taskID.String
		a.Payload = map[string]any{}
		_ = json.Unmarshal([]byte(payload), &a.Payload)
		created, err := timeParseUTC(createdAt)
		if err != nil {
			return nil, err
		}
		a.CreatedAt = created
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
		FROM activity WHERE task_id = $1
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
