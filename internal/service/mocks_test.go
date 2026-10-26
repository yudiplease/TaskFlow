package service_test

import (
	"context"
	"sync"
	"time"

	"tasktracker/internal/domain"
	"tasktracker/internal/events"
	"tasktracker/internal/storage"
)

// mockTaskRepo — память-репозиторий задач для unit-тестов.
type mockTaskRepo struct {
	mu       sync.Mutex
	tasks    map[string]*domain.Task
	byStatus map[domain.Status]float64 // max position
	failOn   map[string]error          // метод → ошибка
	statsRes *domain.Stats
}

func newMockTaskRepo() *mockTaskRepo {
	return &mockTaskRepo{
		tasks:    map[string]*domain.Task{},
		byStatus: map[domain.Status]float64{},
		failOn:   map[string]error{},
	}
}

func (m *mockTaskRepo) Create(_ context.Context, t *domain.Task) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.failOn["Create"]; err != nil {
		return err
	}
	cp := *t
	m.tasks[t.ID] = &cp
	if cp.Position > m.byStatus[cp.Status] {
		m.byStatus[cp.Status] = cp.Position
	}
	return nil
}

func (m *mockTaskRepo) Get(_ context.Context, id string) (*domain.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return nil, errNotFound()
	}
	cp := *t
	return &cp, nil
}

func (m *mockTaskRepo) Update(_ context.Context, t *domain.Task) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tasks[t.ID]; !ok {
		return errNotFound()
	}
	cp := *t
	m.tasks[t.ID] = &cp
	return nil
}

func (m *mockTaskRepo) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tasks[id]; !ok {
		return errNotFound()
	}
	delete(m.tasks, id)
	return nil
}

func (m *mockTaskRepo) List(_ context.Context, f domain.TaskFilter) ([]domain.Task, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Task
	for _, t := range m.tasks {
		if f.Status != nil && t.Status != *f.Status {
			continue
		}
		if f.Assignee != "" && t.Assignee != f.Assignee {
			continue
		}
		cp := *t
		out = append(out, cp)
	}
	return out, len(out), nil
}

func (m *mockTaskRepo) MaxPosition(_ context.Context, st domain.Status) (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.byStatus[st], nil
}

func (m *mockTaskRepo) Stats(_ context.Context) (*domain.Stats, error) {
	if m.statsRes != nil {
		return m.statsRes, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st := &domain.Stats{ByStatus: map[string]int{}, ByPriority: map[string]int{}}
	for _, t := range m.tasks {
		st.Total++
		st.ByStatus[string(t.Status)]++
		st.ByPriority[string(t.Priority)]++
	}
	return st, nil
}

func (m *mockTaskRepo) ListDue(_ context.Context, notAfter time.Time) ([]domain.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Task
	for _, t := range m.tasks {
		if t.Status == domain.StatusDone || t.DueAt == nil {
			continue
		}
		if !t.DueAt.After(notAfter) {
			cp := *t
			out = append(out, cp)
		}
	}
	return out, nil
}

func errNotFound() error { return storage.ErrNotFound }

// mockActivityRepo — память-репозиторий активности.
type mockActivityRepo struct {
	mu    sync.Mutex
	items []domain.Activity
}

func (m *mockActivityRepo) Insert(_ context.Context, a *domain.Activity) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a.ID = int64(len(m.items) + 1)
	m.items = append(m.items, *a)
	return nil
}

func (m *mockActivityRepo) ListByTask(_ context.Context, taskID string, limit int) ([]domain.Activity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Activity
	for i := len(m.items) - 1; i >= 0 && len(out) < limit; i-- {
		if m.items[i].TaskID == taskID {
			out = append(out, m.items[i])
		}
	}
	return out, nil
}

func (m *mockActivityRepo) ListRecent(_ context.Context, limit int) ([]domain.Activity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.Activity, 0, limit)
	for i := len(m.items) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, m.items[i])
	}
	return out, nil
}

// mockPublisher — собирает опубликованные события.
type mockPublisher struct {
	mu     sync.Mutex
	events []domain.Event
}

func (m *mockPublisher) Publish(_ context.Context, e domain.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, e)
}

func (m *mockPublisher) Close() {}

func (m *mockPublisher) types() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.events))
	for i, e := range m.events {
		out[i] = e.Type
	}
	return out
}

var (
	_ events.Publisher = (*mockPublisher)(nil)
)

// mockDefaultsRepo — память-репозиторий «залипающих» значений.
type mockDefaultsRepo struct {
	mu    sync.Mutex
	items map[string]domain.TaskDefaults
}

func newMockDefaultsRepo() *mockDefaultsRepo {
	return &mockDefaultsRepo{items: map[string]domain.TaskDefaults{}}
}

func (m *mockDefaultsRepo) Get(_ context.Context, actor string) (domain.TaskDefaults, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.items[actor]
	if !ok {
		return domain.TaskDefaults{}, time.Time{}, storage.ErrNotFound
	}
	return d, time.Now().UTC(), nil
}

func (m *mockDefaultsRepo) Put(_ context.Context, actor string, d domain.TaskDefaults) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[actor] = d
	return time.Now().UTC(), nil
}

var _ storage.DefaultsRepository = (*mockDefaultsRepo)(nil)

func (m *mockTaskRepo) ListRemindersDue(_ context.Context, now time.Time) ([]domain.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Task
	for _, t := range m.tasks {
		if t.Status == domain.StatusDone || t.DueAt == nil || t.ReminderOffsetMinutes == nil || t.ReminderSentAt != nil {
			continue
		}
		cutoff := t.DueAt.Add(-time.Duration(*t.ReminderOffsetMinutes) * time.Minute)
		if t.DueAt.After(now) && !cutoff.After(now) {  // граница окна включена, как в SQL (<=)
			out = append(out, *t)
		}
	}
	return out, nil
}

func (m *mockTaskRepo) MarkReminderSent(_ context.Context, taskID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tasks[taskID]; !ok {
		return errNotFound()
	}
	if m.tasks[taskID].ReminderSentAt == nil {
		ts := at
		m.tasks[taskID].ReminderSentAt = &ts
	}
	return nil
}
