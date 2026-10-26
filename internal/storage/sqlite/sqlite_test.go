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
		DueAt:       &due,
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
	if got.DueAt == nil || !got.DueAt.Equal(due) {
		t.Errorf("срок = %v, want %v", got.DueAt, due)
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

// TestMigration_005DueAt проверяет миграцию due_date → due_at на базе
// старой схемы (до 005): колонка переименовывается, существующим строкам
// проставляется due_all_day=1 (бизнес-правило: старый фронтенд ставил
// конец дня), данные не теряются.
func TestMigration_005DueAt(t *testing.T) {
	dbPath := t.TempDir() + "/old-schema.db"
	// Строим базу по схеме 001–004 вручную (со старым именем колонки).
	// schema_migrations с записями 001–004 — как в настоящей старой базе:
	// миграционный раннер не должен их повторять.
	setupSQL := `
		CREATE TABLE schema_migrations (name TEXT PRIMARY KEY, applied_at TEXT NOT NULL);
		INSERT INTO schema_migrations (name, applied_at) VALUES
			('001_init.sql', '2026-09-01T00:00:00Z'),
			('002_notifications.sql', '2026-09-01T00:00:01Z'),
			('003_task_colors.sql', '2026-09-01T00:00:02Z'),
			('004_task_defaults.sql', '2026-09-01T00:00:03Z');
		CREATE TABLE tasks (
			id          TEXT PRIMARY KEY,
			title       TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			status      TEXT NOT NULL DEFAULT 'backlog',
			priority    TEXT NOT NULL DEFAULT 'medium',
			assignee    TEXT NOT NULL DEFAULT '',
			labels      TEXT NOT NULL DEFAULT '[]',
			color       TEXT NOT NULL DEFAULT '',
			due_date    TEXT,
			position    REAL NOT NULL DEFAULT 0,
			created_at  TEXT NOT NULL,
			updated_at  TEXT NOT NULL
		);
		INSERT INTO tasks (id, title, due_date, created_at, updated_at)
		VALUES ('11111111-1111-1111-1111-111111111111', 'Старая задача', '2026-10-01T20:59:00Z', '2026-10-01T10:00:00Z', '2026-10-01T10:00:00Z');
	`
	s0, err := sqlite.OpenRawForTest(dbPath, setupSQL)
	if err != nil {
		t.Fatalf("подготовка старой схемы: %v", err)
	}
	_ = s0.Close()

	// Обычное открытие: миграции 001–005 применяются (001–004 — no-op).
	s, err := sqlite.Open(context.Background(), dbPath, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("open с миграцией: %v", err)
	}
	defer func() { _ = s.Close() }()

	repo := sqlite.NewTasks(s)
	got, err := repo.Get(context.Background(), "11111111-1111-1111-1111-111111111111")
	if err != nil {
		t.Fatalf("get после миграции: %v", err)
	}
	if got.DueAt == nil || !got.DueAt.Equal(time.Date(2026, 10, 1, 20, 59, 0, 0, time.UTC)) {
		t.Errorf("due_at = %v, want 2026-10-01T20:59:00Z (данные не должны теряться)", got.DueAt)
	}
	if !got.DueAllDay {
		t.Errorf("старая строка должна получить due_all_day=true")
	}
}
