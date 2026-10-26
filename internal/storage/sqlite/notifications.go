package sqlite

import (
	"context"
	"fmt"
	"time"

	"tasktracker/internal/domain"
	"tasktracker/internal/storage"
)

// Notifications — напоминания в SQLite.
type Notifications struct{ s *Store }

// NewNotifications создаёт репозиторий напоминаний.
func NewNotifications(s *Store) *Notifications { return &Notifications{s} }

// Insert создаёт напоминание, если такого ещё не было (дедуп по ключу).
func (r *Notifications) Insert(ctx context.Context, n *domain.Notification) (bool, error) {
	key := fmt.Sprintf("%s:%s:%s", n.TaskID, n.Type, n.CreatedAt.Format("2006-01-02"))
	res, err := r.s.db.ExecContext(ctx, `
		INSERT INTO notifications (task_id, type, title, message, dedupe_key, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (dedupe_key) DO NOTHING`,
		n.TaskID, n.Type, n.Title, n.Message, key, formatTime(n.CreatedAt))
	if err != nil {
		return false, fmt.Errorf("insert напоминания: %w", err)
	}
	affected, _ := res.RowsAffected()
	return affected > 0, nil
}

// List возвращает последние напоминания.
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

	rows, err := r.s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("select напоминаний: %w", err)
	}
	defer rows.Close()

	out := []domain.Notification{}
	for rows.Next() {
		var n domain.Notification
		var createdAt string
		if err := rows.Scan(&n.ID, &n.TaskID, &n.Type, &n.Title, &n.Message, &n.Read, &createdAt); err != nil {
			return nil, fmt.Errorf("scan напоминания: %w", err)
		}
		created, err := timeParseUTC(createdAt)
		if err != nil {
			return nil, err
		}
		n.CreatedAt = created
		out = append(out, n)
	}
	return out, rows.Err()
}

// MarkRead отмечает одно напоминание прочитанным.
func (r *Notifications) MarkRead(ctx context.Context, id int64) error {
	_, err := r.s.db.ExecContext(ctx,
		`UPDATE notifications SET read_at = $1 WHERE id = $2 AND read_at IS NULL`,
		formatTime(time.Now().UTC()), id)
	if err != nil {
		return fmt.Errorf("mark read: %w", err)
	}
	return nil
}

// MarkAllRead отмечает все напоминания прочитанными.
func (r *Notifications) MarkAllRead(ctx context.Context) error {
	_, err := r.s.db.ExecContext(ctx,
		`UPDATE notifications SET read_at = $1 WHERE read_at IS NULL`, formatTime(time.Now().UTC()))
	if err != nil {
		return fmt.Errorf("mark all read: %w", err)
	}
	return nil
}

// CountUnread считает непрочитанные напоминания.
func (r *Notifications) CountUnread(ctx context.Context) (int, error) {
	var n int
	err := r.s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM notifications WHERE read_at IS NULL`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count unread: %w", err)
	}
	return n, nil
}

var _ storage.NotificationRepository = (*Notifications)(nil)
