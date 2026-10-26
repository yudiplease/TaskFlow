//go:build integration

// Интеграционные тесты хранилища PostgreSQL.
// Запуск: go test -tags integration ./internal/storage/postgres
// с переменной окружения TEST_POSTGRES_DSN
// (по умолчанию postgres://tasktracker:tasktracker@localhost:5432/tasktracker?sslmode=disable).
package postgres_test

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"tasktracker/internal/domain"
	"tasktracker/internal/storage/postgres"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = "postgres://tasktracker:tasktracker@localhost:5432/tasktracker?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Skipf("postgres недоступен (%v), пропуск", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestTaskRepositoryCRUD(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := postgres.NewTasks(pool)

	now := time.Now().UTC().Truncate(time.Second)
	due := now.Add(48 * time.Hour)
	task := &domain.Task{
		ID:          newUUID(),
		Title:       "Интеграционная",
		Description: "проверка CRUD",
		Status:      domain.StatusTodo,
		Priority:    domain.PriorityUrgent,
		Assignee:    "Тестер",
		Labels:      []string{"интеграция", "postgres"},
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
	if got.Title != task.Title || got.Priority != task.Priority || got.Assignee != task.Assignee {
		t.Errorf("полученная задача отличается: %+v", got)
	}
	if !reflect.DeepEqual(got.Labels, task.Labels) {
		t.Errorf("метки = %v, want %v", got.Labels, task.Labels)
	}
	if got.DueDate == nil || !got.DueDate.Equal(due) {
		t.Errorf("срок = %v, want %v", got.DueDate, due)
	}

	got.Status = domain.StatusDone
	got.Position = 2000
	if err := repo.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	again, _ := repo.Get(ctx, task.ID)
	if again.Status != domain.StatusDone {
		t.Errorf("статус после Update = %q", again.Status)
	}

	pos, err := repo.MaxPosition(ctx, domain.StatusDone)
	if err != nil || pos != 2000 {
		t.Errorf("MaxPosition = %v, %v", pos, err)
	}

	items, total, err := repo.List(ctx, domain.TaskFilter{Status: ptrStatus(domain.StatusDone)})
	if err != nil || total != 1 || len(items) != 1 {
		t.Errorf("List = %d/%d, err %v", len(items), total, err)
	}

	if err := repo.Delete(ctx, task.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, task.ID); err == nil {
		t.Error("задача не удалена")
	}
}

func TestActivityRepository(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := postgres.NewActivity(pool)

	act := &domain.Activity{
		TaskID:    "",
		Type:      "task.created",
		Actor:     "интеграционный тест",
		Payload:   map[string]any{"title": "Проверка журнала"},
		CreatedAt: time.Now().UTC(),
	}
	if err := repo.Insert(ctx, act); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if act.ID == 0 {
		t.Error("ID не проставлен")
	}

	items, err := repo.ListRecent(ctx, 5)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("журнал пуст")
	}
	if items[0].Type != "task.created" {
		t.Errorf("тип последней записи = %q", items[0].Type)
	}
}

func ptrStatus(s domain.Status) *domain.Status { return &s }

func newUUID() string {
	// генерируем без зависимости от uuid в тестах хранилища
	b := make([]byte, 16)
	now := time.Now().UnixNano()
	for i := range b {
		b[i] = byte(now >> (i % 8 * 8))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	const hex = "0123456789abcdef"
	out := make([]byte, 36)
	pos := 0
	for i, v := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out[pos] = '-'
			pos++
		}
		out[pos] = hex[v>>4]
		out[pos+1] = hex[v&0x0f]
		pos += 2
	}
	return string(out)
}
