package sqlite_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"tasktracker/internal/domain"
	"tasktracker/internal/storage/sqlite"
)

func testStore(t *testing.T) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(context.Background(), ":memory:", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestTasksCRUDAndCodecs(t *testing.T) {
	s := testStore(t)
	repo := sqlite.NewTasks(s)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Millisecond)
	due := now.Add(48 * time.Hour).Truncate(time.Millisecond)
	task := &domain.Task{
		ID:          "11111111-1111-1111-1111-111111111111",
		Title:       "Локальная задача",
		Description: "проверка SQLite-кодеков",
		Status:      domain.StatusTodo,
		Priority:    domain.PriorityUrgent,
		Assignee:    "Анна",
		Labels:      []string{"desktop", "sqlite"},
		DueDate:     &due,
		Position:    1000,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := repo.Create(ctx, task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != task.Title || got.Assignee != task.Assignee {
		t.Errorf("получена другая задача: %+v", got)
	}
	if !reflect.DeepEqual(got.Labels, task.Labels) {
		t.Errorf("метки = %v, want %v", got.Labels, task.Labels)
	}
	if got.DueDate == nil || !got.DueDate.Equal(due) {
		t.Errorf("срок = %v, want %v", got.DueDate, due)
	}
	if !got.CreatedAt.Equal(now) {
		t.Errorf("создана = %v, want %v", got.CreatedAt, now)
	}

	got.Status = domain.StatusReview
	got.Position = 2048
	if err := repo.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	again, _ := repo.Get(ctx, task.ID)
	if again.Status != domain.StatusReview {
		t.Errorf("статус после Update = %q", again.Status)
	}

	// ListDue видит задачу до горизонта, не видит завершённую.
	dueList, err := repo.ListDue(ctx, due.Add(time.Hour))
	if err != nil || len(dueList) != 1 {
		t.Errorf("ListDue = %d задач, err %v", len(dueList), err)
	}
	again.Status = domain.StatusDone
	_ = repo.Update(ctx, again)
	dueList, _ = repo.ListDue(ctx, due.Add(time.Hour))
	if len(dueList) != 0 {
		t.Errorf("завершённая задача попала в ListDue: %d", len(dueList))
	}

	// Фильтры списка.
	repo2Tasks := []domain.Task{*again}
	_ = repo2Tasks
	items, total, err := repo.List(ctx, domain.TaskFilter{Status: strPtr(domain.StatusDone)})
	if err != nil || total != 1 || len(items) != 1 {
		t.Errorf("List(done) = %d/%d, err %v", len(items), total, err)
	}
	_, _, err = repo.List(ctx, domain.TaskFilter{Search: "sqlite"})
	if err != nil {
		t.Errorf("List(search) err: %v", err)
	}
	_, _, err = repo.List(ctx, domain.TaskFilter{Label: "desktop"})
	if err != nil {
		t.Errorf("List(label) err: %v", err)
	}

	if err := repo.Delete(ctx, task.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, task.ID); err == nil {
		t.Error("задача не удалена")
	}
}

func TestNotificationsDedupe(t *testing.T) {
	s := testStore(t)
	repo := sqlite.NewNotifications(s)
	ctx := context.Background()

	n := &domain.Notification{
		TaskID:    "22222222-2222-2222-2222-222222222222",
		Type:      "due_soon",
		Title:     "Проверка дедупа",
		Message:   "Срок сегодня",
		CreatedAt: time.Now().UTC(),
	}
	created, err := repo.Insert(ctx, n)
	if err != nil || !created {
		t.Fatalf("первый Insert = %v, %v", created, err)
	}
	created2, err := repo.Insert(ctx, n)
	if err != nil {
		t.Fatalf("повторный Insert err: %v", err)
	}
	if created2 {
		t.Error("дедупликация не сработала")
	}

	unread, _ := repo.CountUnread(ctx)
	if unread != 1 {
		t.Errorf("unread = %d, want 1", unread)
	}
	list, _ := repo.List(ctx, false, 10)
	if len(list) != 1 || list[0].Read {
		t.Errorf("List = %+v", list)
	}
	if err := repo.MarkAllRead(ctx); err != nil {
		t.Fatalf("MarkAllRead: %v", err)
	}
	unread, _ = repo.CountUnread(ctx)
	if unread != 0 {
		t.Errorf("unread после MarkAllRead = %d", unread)
	}
}

func TestActivityList(t *testing.T) {
	s := testStore(t)
	repo := sqlite.NewActivity(s)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := repo.Insert(ctx, &domain.Activity{
			Type:      "task.updated",
			Actor:     "тест",
			Payload:   map[string]any{"n": i},
			CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}
	items, err := repo.ListRecent(ctx, 2)
	if err != nil || len(items) != 2 {
		t.Fatalf("ListRecent = %d, err %v", len(items), err)
	}
	if items[0].Payload["n"].(float64) != 2 {
		t.Errorf("порядок нарушен: первым должен быть последний, got payload %v", items[0].Payload["n"])
	}
}

func TestConcurrentAccess(t *testing.T) {
	// Один writer-коннект + busy_timeout: параллельные вставки не должны
	// вызывать "database is locked" или гонки.
	s := testStore(t)
	repo := sqlite.NewTasks(s)
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			now := time.Now().UTC()
			err := repo.Create(ctx, &domain.Task{
				ID:        fmt.Sprintf("33333333-3333-3333-3333-%012d", n),
				Title:     "Параллельная",
				Status:    domain.StatusBacklog,
				Priority:  domain.PriorityMedium,
				Labels:    []string{},
				CreatedAt: now,
				UpdatedAt: now,
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("параллельная вставка: %v", err)
	}
	_, total, err := repo.List(ctx, domain.TaskFilter{})
	if err != nil || total != 20 {
		t.Errorf("total = %d, err %v", total, err)
	}
}

func strPtr(s domain.Status) *domain.Status { return &s }
