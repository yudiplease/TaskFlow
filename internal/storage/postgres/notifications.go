package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"tasktracker/internal/domain"
	"tasktracker/internal/storage"
)

// Notifications — репозиторий напоминаний на PostgreSQL.
type Notifications struct {
	pool *pgxpool.Pool
}

// NewNotifications создаёт репозиторий напоминаний.
func NewNotifications(pool *pgxpool.Pool) *Notifications { return &Notifications{pool: pool} }

// Insert создаёт напоминание, если такого ещё не было (дедуп по ключу).
func (r *Notifications) Insert(ctx context.Context, n *domain.Notification) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO notifications (task_id, type, title, message, dedupe_key, created_at)
		VALUES ($1::uuid, $2, $3, $4, $5, $6)
		ON CONFLICT (dedupe_key) DO NOTHING`,
		n.TaskID, n.Type, n.Title, n.Message, dedupeKey(n), n.CreatedAt)
	if err != nil {
		return false, fmt.Errorf("insert напоминания: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func dedupeKey(n *domain.Notification) string {
	return fmt.Sprintf("%s:%s:%s", n.TaskID, n.Type, n.CreatedAt.Format("2006-01-02"))
}

func (r *Notifications) List(ctx context.Context, onlyUnread bool, limit int) ([]domain.Notification, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT id, task_id, type, title, message, read_at IS NOT NULL, created_at
	      FROM notifications`
	args := []any{}
	if onlyUnread {
		q += ` WHERE read_at IS NULL`
	}
	q += ` ORDER BY id DESC LIMIT $1`
	args = append(args, limit)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("select напоминаний: %w", err)
	}
	defer rows.Close()

	out := []domain.Notification{}
	for rows.Next() {
		var n domain.Notification
		if err := rows.Scan(&n.ID, &n.TaskID, &n.Type, &n.Title, &n.Message, &n.Read, &n.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan напоминания: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// MarkRead отмечает одно напоминание прочитанным.
func (r *Notifications) MarkRead(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `UPDATE notifications SET read_at = now() WHERE id = $1 AND read_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("mark read: %w", err)
	}
	return nil
}

// MarkAllRead отмечает все напоминания прочитанными.
func (r *Notifications) MarkAllRead(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `UPDATE notifications SET read_at = now() WHERE read_at IS NULL`)
	if err != nil {
		return fmt.Errorf("mark all read: %w", err)
	}
	return nil
}

// CountUnread считает непрочитанные напоминания.
func (r *Notifications) CountUnread(ctx context.Context) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE read_at IS NULL`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count unread: %w", err)
	}
	return n, nil
}

var _ storage.NotificationRepository = (*Notifications)(nil)
