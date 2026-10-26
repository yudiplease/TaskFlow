package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"tasktracker/internal/domain"
	"tasktracker/internal/storage"
)

// Tasks — репозиторий задач в SQLite.
type Tasks struct{ s *Store }

// NewTasks создаёт репозиторий задач.
func NewTasks(s *Store) *Tasks { return &Tasks{s} }

const taskColumns = `id, title, description, status, priority, assignee, labels, color, due_at, due_all_day, due_timezone, reminder_offset_minutes, reminder_sent_at, position, created_at, updated_at`

func scanTask(row interface{ Scan(...any) error }) (*domain.Task, error) {
	var t domain.Task
	var labels, createdAt, updatedAt string
	var due, reminderSent sql.NullString
	var reminderOffset sql.NullInt64
	if err := row.Scan(&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.Assignee,
		&labels, &t.Color, &due, &t.DueAllDay, &t.DueTimezone, &reminderOffset, &reminderSent,
		&t.Position, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("scan задачи: %w", err)
	}
	t.Labels = parseLabels(labels)
	created, err := timeParseUTC(createdAt)
	if err != nil {
		return nil, err
	}
	updated, err := timeParseUTC(updatedAt)
	if err != nil {
		return nil, err
	}
	t.CreatedAt, t.UpdatedAt = created, updated
	if due.Valid && due.String != "" {
		d, err := timeParseUTC(due.String)
		if err != nil {
			return nil, err
		}
		t.DueAt = &d
	}
	if reminderOffset.Valid {
		v := int(reminderOffset.Int64)
		t.ReminderOffsetMinutes = &v
	}
	if reminderSent.Valid && reminderSent.String != "" {
		sent, err := timeParseUTC(reminderSent.String)
		if err != nil {
			return nil, err
		}
		t.ReminderSentAt = &sent
	}
	return &t, nil
}

// Create вставляет новую задачу.
func (r *Tasks) Create(ctx context.Context, t *domain.Task) error {
	_, err := r.s.db.ExecContext(ctx, `
		INSERT INTO tasks (id, title, description, status, priority, assignee, labels, color,
			due_at, due_all_day, due_timezone, reminder_offset_minutes, reminder_sent_at, position, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		t.ID, t.Title, t.Description, t.Status, t.Priority, t.Assignee, formatLabels(t.Labels),
		t.Color, formatDue(t.DueAt), boolToInt(t.DueAllDay), t.DueTimezone,
		nullableInt(t.ReminderOffsetMinutes), formatNullableTime(t.ReminderSentAt),
		t.Position, formatTime(t.CreatedAt), formatTime(t.UpdatedAt))
	if err != nil {
		return fmt.Errorf("insert задачи: %w", err)
	}
	return nil
}

// Get возвращает задачу по идентификатору.
func (r *Tasks) Get(ctx context.Context, id string) (*domain.Task, error) {
	row := r.s.db.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id = $1`, id)
	return scanTask(row)
}

// Update перезаписывает поля задачи.
func (r *Tasks) Update(ctx context.Context, t *domain.Task) error {
	res, err := r.s.db.ExecContext(ctx, `
		UPDATE tasks SET
			title = $2, description = $3, status = $4, priority = $5,
			assignee = $6, labels = $7, color = $8, due_at = $9, due_all_day = $10,
			due_timezone = $11, reminder_offset_minutes = $12, reminder_sent_at = $13,
			position = $14, updated_at = $15
		WHERE id = $1`,
		t.ID, t.Title, t.Description, t.Status, t.Priority, t.Assignee, formatLabels(t.Labels),
		t.Color, formatDue(t.DueAt), boolToInt(t.DueAllDay), t.DueTimezone,
		nullableInt(t.ReminderOffsetMinutes), formatNullableTime(t.ReminderSentAt),
		t.Position, formatTime(t.UpdatedAt))
	if err != nil {
		return fmt.Errorf("update задачи: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

// Delete удаляет задачу.
func (r *Tasks) Delete(ctx context.Context, id string) error {
	res, err := r.s.db.ExecContext(ctx, `DELETE FROM tasks WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete задачи: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

// List выбирает задачи по фильтру и возвращает общее число подходящих.
func (r *Tasks) List(ctx context.Context, f domain.TaskFilter) ([]domain.Task, int, error) {
	var where []string
	var args []any

	if f.Status != nil {
		args = append(args, string(*f.Status))
		where = append(where, fmt.Sprintf("status = $%d", len(args)))
	}
	if f.Priority != nil {
		args = append(args, string(*f.Priority))
		where = append(where, fmt.Sprintf("priority = $%d", len(args)))
	}
	if f.Assignee != "" {
		args = append(args, f.Assignee)
		where = append(where, fmt.Sprintf("assignee = $%d", len(args)))
	}
	if f.Label != "" {
		args = append(args, f.Label)
		where = append(where, fmt.Sprintf(
			`EXISTS (SELECT 1 FROM json_each(tasks.labels) je WHERE je.value = $%d)`, len(args)))
	}
	if f.Color != "" {
		args = append(args, f.Color)
		where = append(where, fmt.Sprintf("color = $%d", len(args)))
	}
	if f.Search != "" {
		args = append(args, "%"+strings.ToLower(f.Search)+"%")
		idx := len(args)
		where = append(where, fmt.Sprintf("(lower(title) LIKE $%d OR lower(description) LIKE $%d)", idx, idx))
	}

	clause := ""
	if len(where) > 0 {
		clause = "WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := r.s.db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT count(*) FROM tasks %s", clause), args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count задач: %w", err)
	}

	limit, offset := f.Limit, f.Offset
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := r.s.db.QueryContext(ctx, fmt.Sprintf(
		`SELECT %s FROM tasks %s ORDER BY position ASC, created_at ASC LIMIT %d OFFSET %d`,
		taskColumns, clause, limit, offset), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("select задач: %w", err)
	}
	defer rows.Close()

	out := []domain.Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *t)
	}
	return out, total, rows.Err()
}

// MaxPosition возвращает максимальную позицию в колонке.
func (r *Tasks) MaxPosition(ctx context.Context, status domain.Status) (float64, error) {
	var pos float64
	err := r.s.db.QueryRowContext(ctx,
		`SELECT COALESCE(max(position), 0) FROM tasks WHERE status = $1`, string(status)).Scan(&pos)
	if err != nil {
		return 0, fmt.Errorf("max position: %w", err)
	}
	return pos, nil
}

// ListDue возвращает незавершённые задачи со сроком не позже notAfter.
func (r *Tasks) ListDue(ctx context.Context, notAfter time.Time) ([]domain.Task, error) {
	rows, err := r.s.db.QueryContext(ctx, `
		SELECT `+taskColumns+` FROM tasks
		WHERE due_at IS NOT NULL AND due_at <> '' AND status <> 'done' AND due_at <= $1
		ORDER BY due_at ASC LIMIT 500`, formatTime(notAfter))
	if err != nil {
		return nil, fmt.Errorf("select due задач: %w", err)
	}
	defer rows.Close()

	out := []domain.Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// ListRemindersDue возвращает задачи, по которым пора напомнить персонально:
// смещение задано, напоминание ещё не отправлялось и до срока осталось
// не больше offset минут (срок ещё не наступил — наступивший срок
// обслуживается потоком due_soon/overdue).
// datetime() нормализует обе стороны сравнения: due_at хранится RFC3339-строкой.
func (r *Tasks) ListRemindersDue(ctx context.Context, now time.Time) ([]domain.Task, error) {
	rows, err := r.s.db.QueryContext(ctx, `
		SELECT `+taskColumns+` FROM tasks
		WHERE reminder_offset_minutes IS NOT NULL AND reminder_offset_minutes > 0
		  AND reminder_sent_at IS NULL AND status <> 'done'
		  AND due_at IS NOT NULL AND due_at <> ''
		  AND datetime(due_at) > datetime($1)
		  AND datetime(due_at, '-' || reminder_offset_minutes || ' minutes') <= datetime($1)
		ORDER BY due_at ASC LIMIT 500`, formatTime(now))
	if err != nil {
		return nil, fmt.Errorf("select reminders due: %w", err)
	}
	defer rows.Close()

	out := []domain.Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// MarkReminderSent фиксирует отправку напоминания (идемпотентно).
func (r *Tasks) MarkReminderSent(ctx context.Context, taskID string, at time.Time) error {
	_, err := r.s.db.ExecContext(ctx,
		`UPDATE tasks SET reminder_sent_at = $2 WHERE id = $1 AND reminder_sent_at IS NULL`,
		taskID, formatTime(at))
	if err != nil {
		return fmt.Errorf("mark reminder sent: %w", err)
	}
	return nil
}

// Stats считает агрегаты для дашборда.
func (r *Tasks) Stats(ctx context.Context) (*domain.Stats, error) {
	s := &domain.Stats{ByStatus: map[string]int{}, ByPriority: map[string]int{}}
	err := r.s.db.QueryRowContext(ctx, `
		SELECT
			count(*),
			count(*) FILTER (WHERE due_at IS NOT NULL AND due_at <> '' AND datetime(due_at) < datetime('now') AND status <> 'done'),
			count(*) FILTER (WHERE created_at >= date('now')),
			count(*) FILTER (WHERE status = 'done' AND updated_at >= date('now')),
			count(*) FILTER (WHERE assignee = '')
		FROM tasks`).Scan(&s.Total, &s.Overdue, &s.CreatedToday, &s.DoneToday, &s.Unassigned)
	if err != nil {
		return nil, fmt.Errorf("stats total: %w", err)
	}

	rows, err := r.s.db.QueryContext(ctx, `SELECT status, count(*) FROM tasks GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("stats by status: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		s.ByStatus[st] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows2, err := r.s.db.QueryContext(ctx, `SELECT priority, count(*) FROM tasks GROUP BY priority`)
	if err != nil {
		return nil, fmt.Errorf("stats by priority: %w", err)
	}
	defer rows2.Close()
	for rows2.Next() {
		var p string
		var n int
		if err := rows2.Scan(&p, &n); err != nil {
			return nil, err
		}
		s.ByPriority[p] = n
	}
	return s, rows2.Err()
}

func formatDue(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

func formatNullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

func nullableInt(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// timeParseUTC разбирает строку времени из SQLite.
func timeParseUTC(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}

var _ storage.TaskRepository = (*Tasks)(nil)
