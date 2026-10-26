// Package domain содержит бизнес-модели трекера задач.
package domain

import (
	"errors"
	"time"
)

// Status — колонка канбан-доски.
type Status string

const (
	StatusBacklog    Status = "backlog"
	StatusTodo       Status = "todo"
	StatusInProgress Status = "in_progress"
	StatusReview     Status = "review"
	StatusDone       Status = "done"
)

// Statuses сохраняет порядок колонок на доске.
var Statuses = []Status{StatusBacklog, StatusTodo, StatusInProgress, StatusReview, StatusDone}

func (s Status) Valid() bool {
	switch s {
	case StatusBacklog, StatusTodo, StatusInProgress, StatusReview, StatusDone:
		return true
	}
	return false
}

// Priority — срочность задачи.
type Priority string

const (
	PriorityLow    Priority = "low"
	PriorityMedium Priority = "medium"
	PriorityHigh   Priority = "high"
	PriorityUrgent Priority = "urgent"
)

func (p Priority) Valid() bool {
	switch p {
	case PriorityLow, PriorityMedium, PriorityHigh, PriorityUrgent:
		return true
	}
	return false
}

// Task — карточка на доске. Position задаёт порядок внутри колонки
// (дробная нумерация: вставка между соседями = середина отрезка).
type Task struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Status      Status     `json:"status"`
	Priority    Priority   `json:"priority"`
	Assignee    string     `json:"assignee"`
	Labels      []string   `json:"labels"`
	Color       string     `json:"color,omitempty"` // HEX из справочника ColorTags; пусто = без метки
	DueDate     *time.Time `json:"due_date,omitempty"`
	Position    float64    `json:"position"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// TaskFilter — параметры выборки списка задач.
type TaskFilter struct {
	Status   *Status
	Priority *Priority
	Assignee string
	Label    string
	Color    string // канонический HEX из справочника; пусто = без фильтра
	Search   string
	Limit    int
	Offset   int
}

// Activity — запись журнала событий (пишется консьюмером Kafka).
type Activity struct {
	ID        int64          `json:"id"`
	TaskID    string         `json:"task_id,omitempty"`
	Type      string         `json:"type"`
	Actor     string         `json:"actor"`
	Payload   map[string]any `json:"payload"`
	CreatedAt time.Time      `json:"created_at"`
}

// Notification — напоминание о задаче (создаётся планировщиком).
type Notification struct {
	ID        int64     `json:"id"`
	TaskID    string    `json:"task_id"`
	Type      string    `json:"type"` // due_soon | overdue
	Title     string    `json:"title"`
	Message   string    `json:"message"`
	Read      bool      `json:"read"`
	CreatedAt time.Time `json:"created_at"`
}

// Stats — сводка для дашборда.
type Stats struct {
	Total        int            `json:"total"`
	ByStatus     map[string]int `json:"by_status"`
	ByPriority   map[string]int `json:"by_priority"`
	Overdue      int            `json:"overdue"`
	CreatedToday int            `json:"created_today"`
	DoneToday    int            `json:"done_today"`
	Unassigned   int            `json:"unassigned"`
}

// Event — событие о задаче, публикуемое в Kafka.
type Event struct {
	Type      string         `json:"type"` // task.created | task.updated | task.status_changed | task.deleted
	TaskID    string         `json:"task_id"`
	Actor     string         `json:"actor"`
	Payload   map[string]any `json:"payload"`
	Timestamp time.Time      `json:"timestamp"`
}

// Ошибки домена.
var (
	ErrNotFound = errors.New("задача не найдена")
	ErrInvalid  = errors.New("некорректные данные")
)
