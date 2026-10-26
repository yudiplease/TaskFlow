// Package service содержит бизнес-логику трекера задач.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"tasktracker/internal/domain"
	"tasktracker/internal/events"
	"tasktracker/internal/metrics"
	"tasktracker/internal/storage"
)

// Названия событий Kafka.
const (
	EventTaskCreated       = "task.created"
	EventTaskUpdated       = "task.updated"
	EventTaskStatusChanged = "task.status_changed"
	EventTaskDeleted       = "task.deleted"
)

// Actor по умолчанию, если заголовок X-Actor не передан.
const DefaultActor = "system"

// TaskService — сценарии над задачами.
type TaskService struct {
	tasks    storage.TaskRepository
	activity storage.ActivityRepository
	pub      events.Publisher
	metrics  *metrics.Metrics
}

// NewTaskService собирает сервис.
func NewTaskService(tasks storage.TaskRepository, activity storage.ActivityRepository, pub events.Publisher, m *metrics.Metrics) *TaskService {
	return &TaskService{tasks: tasks, activity: activity, pub: pub, metrics: m}
}

// CreateTaskInput — данные для создания задачи.
type CreateTaskInput struct {
	Title       string
	Description string
	Status      domain.Status
	Priority    domain.Priority
	Assignee    string
	Labels      []string
	Color       string // HEX из справочника domain.ColorTags; пусто = без метки
	DueDate     *time.Time
}

// UpdateTaskInput — частичное обновление; nil = не менять поле.
// ClearDueDate=true сбрасывает срок (фронтенд шлёт due_date="" для очистки).
// Color — указатель: пустая строка сбрасывает метку, nil оставляет как было.
type UpdateTaskInput struct {
	Title        *string
	Description  *string
	Status       *domain.Status
	Priority     *domain.Priority
	Assignee     *string
	Labels       *[]string
	Color        *string
	DueDate      *time.Time
	ClearDueDate bool
	Position     *float64
}

// Create создаёт задачу и публикует событие task.created.
func (s *TaskService) Create(ctx context.Context, in CreateTaskInput, actor string) (*domain.Task, error) {
	in.Title = strings.TrimSpace(in.Title)
	if err := validateTitle(in.Title); err != nil {
		return nil, err
	}
	if len(in.Description) > 5000 {
		return nil, fmt.Errorf("%w: описание длиннее 5000 символов", domain.ErrInvalid)
	}
	if in.Status == "" {
		in.Status = domain.StatusBacklog
	}
	if !in.Status.Valid() {
		return nil, fmt.Errorf("%w: неизвестный статус %q", domain.ErrInvalid, in.Status)
	}
	if in.Priority == "" {
		in.Priority = domain.PriorityMedium
	}
	if !in.Priority.Valid() {
		return nil, fmt.Errorf("%w: неизвестный приоритет %q", domain.ErrInvalid, in.Priority)
	}
	labels, err := normalizeLabels(in.Labels)
	if err != nil {
		return nil, err
	}
	color, err := domain.NormalizeColor(in.Color)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	pos, err := s.tasks.MaxPosition(ctx, in.Status)
	if err != nil {
		return nil, err
	}

	t := &domain.Task{
		ID:          uuid.NewString(),
		Title:       in.Title,
		Description: strings.TrimSpace(in.Description),
		Status:      in.Status,
		Priority:    in.Priority,
		Assignee:    strings.TrimSpace(in.Assignee),
		Labels:      labels,
		Color:       color,
		DueDate:     in.DueDate,
		Position:    pos + 1000,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.tasks.Create(ctx, t); err != nil {
		return nil, err
	}

	s.pub.Publish(ctx, domain.Event{
		Type:      EventTaskCreated,
		TaskID:    t.ID,
		Actor:     actor,
		Timestamp: now,
		Payload: map[string]any{
			"title":    t.Title,
			"status":   string(t.Status),
			"priority": string(t.Priority),
			"assignee": t.Assignee,
		},
	})
	s.observe("create", t.Status)
	return t, nil
}

// Get возвращает задачу по ID.
func (s *TaskService) Get(ctx context.Context, id string) (*domain.Task, error) {
	return s.tasks.Get(ctx, id)
}

// List выбирает задачи по фильтру.
func (s *TaskService) List(ctx context.Context, f domain.TaskFilter) ([]domain.Task, int, error) {
	return s.tasks.List(ctx, f)
}

// Update применяет частичное изменение и публикует события
// task.updated и/или task.status_changed.
func (s *TaskService) Update(ctx context.Context, id string, in UpdateTaskInput, actor string) (*domain.Task, error) {
	t, err := s.tasks.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	// Исходный статус сохраняем до изменений, чтобы опубликовать from/to.
	oldStatus := t.Status

	if in.Title != nil {
		title := strings.TrimSpace(*in.Title)
		if err := validateTitle(title); err != nil {
			return nil, err
		}
		t.Title = title
	}
	if in.Description != nil {
		d := *in.Description
		if len(d) > 5000 {
			return nil, fmt.Errorf("%w: описание длиннее 5000 символов", domain.ErrInvalid)
		}
		t.Description = strings.TrimSpace(d)
	}
	if in.Status != nil {
		if !in.Status.Valid() {
			return nil, fmt.Errorf("%w: неизвестный статус %q", domain.ErrInvalid, *in.Status)
		}
		t.Status = *in.Status
	}
	if in.Priority != nil {
		if !in.Priority.Valid() {
			return nil, fmt.Errorf("%w: неизвестный приоритет %q", domain.ErrInvalid, *in.Priority)
		}
		t.Priority = *in.Priority
	}
	if in.Assignee != nil {
		t.Assignee = strings.TrimSpace(*in.Assignee)
	}
	if in.Labels != nil {
		labels, err := normalizeLabels(*in.Labels)
		if err != nil {
			return nil, err
		}
		t.Labels = labels
	}
	if in.Color != nil {
		color, err := domain.NormalizeColor(*in.Color)
		if err != nil {
			return nil, err
		}
		t.Color = color
	}
	if in.DueDate != nil {
		t.DueDate = in.DueDate
	}
	if in.ClearDueDate {
		t.DueDate = nil
	}
	if in.Position != nil {
		t.Position = *in.Position
	}

	previous := oldStatus
	t.UpdatedAt = time.Now().UTC()
	if err := s.tasks.Update(ctx, t); err != nil {
		return nil, err
	}

	now := t.UpdatedAt
	if in.Status != nil {
		s.pub.Publish(ctx, domain.Event{
			Type:      EventTaskStatusChanged,
			TaskID:    t.ID,
			Actor:     actor,
			Timestamp: now,
			Payload: map[string]any{
				"title": t.Title,
				"from":  string(previous),
				"to":    string(t.Status),
			},
		})
		s.observe("move", t.Status)
	}
	s.pub.Publish(ctx, domain.Event{
		Type:      EventTaskUpdated,
		TaskID:    t.ID,
		Actor:     actor,
		Timestamp: now,
		Payload: map[string]any{
			"title":    t.Title,
			"status":   string(t.Status),
			"priority": string(t.Priority),
			"assignee": t.Assignee,
		},
	})
	s.observe("update", t.Status)
	return t, nil
}

// Move переносит задачу в колонку на заданную позицию (drag & drop).
func (s *TaskService) Move(ctx context.Context, id string, status domain.Status, position float64, actor string) (*domain.Task, error) {
	if !status.Valid() {
		return nil, fmt.Errorf("%w: неизвестный статус %q", domain.ErrInvalid, status)
	}
	return s.Update(ctx, id, UpdateTaskInput{
		Status:   &status,
		Position: &position,
	}, actor)
}

// Delete удаляет задачу и публикует событие task.deleted.
func (s *TaskService) Delete(ctx context.Context, id string, actor string) error {
	t, err := s.tasks.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := s.tasks.Delete(ctx, id); err != nil {
		return err
	}
	s.pub.Publish(ctx, domain.Event{
		Type:      EventTaskDeleted,
		TaskID:    id,
		Actor:     actor,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"title": t.Title,
		},
	})
	s.observe("delete", t.Status)
	return nil
}

// Activity возвращает журнал по задаче.
func (s *TaskService) Activity(ctx context.Context, taskID string, limit int) ([]domain.Activity, error) {
	return s.activity.ListByTask(ctx, taskID, limit)
}

// RecentActivity возвращает общий журнал событий.
func (s *TaskService) RecentActivity(ctx context.Context, limit int) ([]domain.Activity, error) {
	return s.activity.ListRecent(ctx, limit)
}

// Stats считает агрегаты и обновляет gauge tasks_by_status.
func (s *TaskService) Stats(ctx context.Context) (*domain.Stats, error) {
	stt, err := s.tasks.Stats(ctx)
	if err != nil {
		return nil, err
	}
	for _, st := range domain.Statuses {
		s.metrics.TasksByStatus.WithLabelValues(string(st)).Set(float64(stt.ByStatus[string(st)]))
	}
	return stt, nil
}

func (s *TaskService) observe(op string, st domain.Status) {
	s.metrics.TaskOperationsTotal.WithLabelValues(op, string(st)).Inc()
}

func validateTitle(title string) error {
	if title == "" {
		return fmt.Errorf("%w: заголовок обязателен", domain.ErrInvalid)
	}
	if len(title) > 200 {
		return fmt.Errorf("%w: заголовок длиннее 200 символов", domain.ErrInvalid)
	}
	return nil
}

func normalizeLabels(labels []string) ([]string, error) {
	if len(labels) > 10 {
		return nil, fmt.Errorf("%w: не больше 10 меток", domain.ErrInvalid)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if len(l) > 30 {
			return nil, fmt.Errorf("%w: метка длиннее 30 символов", domain.ErrInvalid)
		}
		low := strings.ToLower(l)
		if !seen[low] {
			seen[low] = true
			out = append(out, l)
		}
	}
	return out, nil
}

// ErrNotFound и ErrInvalid переэкспортируются для удобства обработчиков.
var (
	ErrNotFound = storage.ErrNotFound
	ErrInvalid  = domain.ErrInvalid
)

// IsNotFound — проверка ошибки «не найдено».
func IsNotFound(err error) bool { return errors.Is(err, storage.ErrNotFound) }
