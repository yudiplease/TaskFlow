// Package storage описывает интерфейсы хранилища задач.
package storage

import (
	"context"
	"errors"
	"time"

	"tasktracker/internal/domain"
)

// ErrNotFound возвращается, когда запись отсутствует.
var ErrNotFound = errors.New("not found")

// TaskRepository — доступ к задачам.
type TaskRepository interface {
	Create(ctx context.Context, t *domain.Task) error
	Get(ctx context.Context, id string) (*domain.Task, error)
	Update(ctx context.Context, t *domain.Task) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, f domain.TaskFilter) ([]domain.Task, int, error)
	MaxPosition(ctx context.Context, status domain.Status) (float64, error)
	Stats(ctx context.Context) (*domain.Stats, error)
	// ListDue возвращает незавершённые задачи со сроком не позже notAfter.
	ListDue(ctx context.Context, notAfter time.Time) ([]domain.Task, error)
}

// ActivityRepository — журнал событий.
type ActivityRepository interface {
	Insert(ctx context.Context, a *domain.Activity) error
	ListByTask(ctx context.Context, taskID string, limit int) ([]domain.Activity, error)
	ListRecent(ctx context.Context, limit int) ([]domain.Activity, error)
}

// NotificationRepository — напоминания о задачах.
type NotificationRepository interface {
	// Insert создаёт напоминание; created=false, если оно уже есть (дедупликация).
	Insert(ctx context.Context, n *domain.Notification) (created bool, err error)
	List(ctx context.Context, onlyUnread bool, limit int) ([]domain.Notification, error)
	MarkRead(ctx context.Context, id int64) error
	MarkAllRead(ctx context.Context) error
	CountUnread(ctx context.Context) (int, error)
}

// DefaultsRepository — «залипающие» значения формы новой задачи (per-user).
// Хранятся как нормализованный JSON domain.TaskDefaults.
type DefaultsRepository interface {
	// Get возвращает значения и время обновления; ErrNotFound, если их ещё нет.
	Get(ctx context.Context, actor string) (domain.TaskDefaults, time.Time, error)
	// Put сохраняет значения и возвращает время записи (источник истины для гонок).
	Put(ctx context.Context, actor string, d domain.TaskDefaults) (time.Time, error)
}
