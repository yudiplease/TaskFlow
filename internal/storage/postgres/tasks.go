package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tasktracker/internal/domain"
	"tasktracker/internal/storage"
)

// Tasks — репозиторий задач поверх pgx.
type Tasks struct {
	pool *pgxpool.Pool
}

// NewTasks создаёт репозиторий задач.
func NewTasks(pool *pgxpool.Pool) *Tasks { return &Tasks{pool: pool} }

const taskColumns = `id, title, description, status, priority, assignee, labels, color, due_date, position, created_at, updated_at`

func scanTask(row pgx.Row) (*domain.Task, error) {
	var t domain.Task
	err := row.Scan(&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.Assignee, &t.Labels, &t.Color, &t.DueDate, &t.Position, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan задачи: %w", err)
	}
	return &t, nil
}

// Create вставляет новую задачу.
func (r *Tasks) Create(ctx context.Context, t *domain.Task) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO tasks (id, title, description, status, priority, assignee, labels, color, due_date, position, created_at, updated_at)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		t.ID, t.Title, t.Description, t.Status, t.Priority, t.Assignee, t.Labels, t.Color, t.DueDate, t.Position, t.CreatedAt, t.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert задачи: %w", err)
	}
	return nil
}

// Get возвращает задачу по идентификатору.
func (r *Tasks) Get(ctx context.Context, id string) (*domain.Task, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id = $1::uuid`, id)
	return scanTask(row)
}

// Update перезаписывает поля задачи.
func (r *Tasks) Update(ctx context.Context, t *domain.Task) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE tasks SET
			title = $2, description = $3, status = $4, priority = $5,
			assignee = $6, labels = $7, color = $8, due_date = $9, position = $10, updated_at = $11
		WHERE id = $1::uuid`,
		t.ID, t.Title, t.Description, t.Status, t.Priority, t.Assignee, t.Labels, t.Color, t.DueDate, t.Position, t.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update задачи: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrNotFound
	}
	return nil
}

// Delete удаляет задачу.
func (r *Tasks) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM tasks WHERE id = $1::uuid`, id)
	if err != nil {
		return fmt.Errorf("delete задачи: %w", err)
	}
	if tag.RowsAffected() == 0 {
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
		where = append(where, fmt.Sprintf("$%d = ANY(labels)", len(args)))
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
	q := fmt.Sprintf("SELECT count(*) FROM tasks %s", clause)
	if err := r.pool.QueryRow(ctx, q, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count задач: %w", err)
	}

	limit, offset := f.Limit, f.Offset
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}

	q = fmt.Sprintf("SELECT %s FROM tasks %s ORDER BY position ASC, created_at ASC LIMIT %d OFFSET %d", taskColumns, clause, limit, offset)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("select задач: %w", err)
	}
	defer rows.Close()

	var out []domain.Task
	for rows.Next() {
		var t domain.Task
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.Assignee, &t.Labels, &t.DueDate, &t.Position, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan списка задач: %w", err)
		}
		out = append(out, t)
	}
	return out, total, rows.Err()
}

// MaxPosition возвращает максимальную позицию в колонке (0, если пусто).
func (r *Tasks) MaxPosition(ctx context.Context, status domain.Status) (float64, error) {
	var pos float64
	err := r.pool.QueryRow(ctx, `SELECT COALESCE(max(position), 0) FROM tasks WHERE status = $1`, string(status)).Scan(&pos)
	if err != nil {
		return 0, fmt.Errorf("max position: %w", err)
	}
	return pos, nil
}

// ListDue возвращает незавершённые задачи со сроком не позже notAfter.
func (r *Tasks) ListDue(ctx context.Context, notAfter time.Time) ([]domain.Task, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+taskColumns+` FROM tasks
		WHERE due_date IS NOT NULL AND status <> 'done' AND due_date <= $1
		ORDER BY due_date ASC LIMIT 500`, notAfter)
	if err != nil {
		return nil, fmt.Errorf("select due задач: %w", err)
	}
	defer rows.Close()

	out := []domain.Task{}
	for rows.Next() {
		var t domain.Task
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.Assignee, &t.Labels, &t.DueDate, &t.Position, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan due задачи: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Stats считает агрегаты для дашборда одним запросом.
func (r *Tasks) Stats(ctx context.Context) (*domain.Stats, error) {
	s := &domain.Stats{
		ByStatus:   map[string]int{},
		ByPriority: map[string]int{},
	}
	err := r.pool.QueryRow(ctx, `
		SELECT
			count(*),
			count(*) FILTER (WHERE due_date IS NOT NULL AND due_date < now() AND status <> 'done'),
			count(*) FILTER (WHERE created_at >= date_trunc('day', now())),
			count(*) FILTER (WHERE status = 'done' AND updated_at >= date_trunc('day', now())),
			count(*) FILTER (WHERE assignee = '')
		FROM tasks`).Scan(&s.Total, &s.Overdue, &s.CreatedToday, &s.DoneToday, &s.Unassigned)
	if err != nil {
		return nil, fmt.Errorf("stats total: %w", err)
	}

	rows, err := r.pool.Query(ctx, `SELECT status, count(*) FROM tasks GROUP BY status`)
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

	rows2, err := r.pool.Query(ctx, `SELECT priority, count(*) FROM tasks GROUP BY priority`)
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

var _ storage.TaskRepository = (*Tasks)(nil)
