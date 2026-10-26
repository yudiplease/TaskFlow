package service_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"tasktracker/internal/domain"
	"tasktracker/internal/metrics"
	"tasktracker/internal/service"
	"tasktracker/internal/storage"
)

func newService(t *testing.T) (*service.TaskService, *mockTaskRepo, *mockActivityRepo, *mockPublisher) {
	t.Helper()
	repo := newMockTaskRepo()
	act := &mockActivityRepo{}
	pub := &mockPublisher{}
	svc := service.NewTaskService(repo, act, pub, metrics.New())
	return svc, repo, act, pub
}

func TestCreate_OK(t *testing.T) {
	svc, repo, _, pub := newService(t)
	ctx := context.Background()

	due := time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)
	task, err := svc.Create(ctx, service.CreateTaskInput{
		Title:    "  Настроить мониторинг  ",
		Status:   domain.StatusTodo,
		Priority: domain.PriorityHigh,
		Assignee: " Анна ",
		Labels:   []string{"devops", " DevOps ", ""},
		DueDate:  &due,
	}, "Вы")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if task.Title != "Настроить мониторинг" {
		t.Errorf("заголовок не обрезан: %q", task.Title)
	}
	if task.Assignee != "Анна" {
		t.Errorf("исполнитель не обрезан: %q", task.Assignee)
	}
	// Дедупликация регистронезависима: остаётся первое написание.
	if want := []string{"devops"}; !reflect.DeepEqual(task.Labels, want) {
		t.Errorf("метки = %v, want %v", task.Labels, want)
	}
	if task.Position != 1000 {
		t.Errorf("позиция первой задачи = %v, want 1000", task.Position)
	}
	if _, err := repo.Get(ctx, task.ID); err != nil {
		t.Errorf("задача не сохранилась: %v", err)
	}
	if got := pub.types(); !reflect.DeepEqual(got, []string{service.EventTaskCreated}) {
		t.Errorf("события = %v, want [task.created]", got)
	}
}

func TestCreate_Defaults(t *testing.T) {
	svc, _, _, _ := newService(t)
	task, err := svc.Create(context.Background(), service.CreateTaskInput{Title: "Пороговая"}, "system")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if task.Status != domain.StatusBacklog {
		t.Errorf("статус по умолчанию = %q, want backlog", task.Status)
	}
	if task.Priority != domain.PriorityMedium {
		t.Errorf("приоритет по умолчанию = %q, want medium", task.Priority)
	}
}

func TestCreate_Validation(t *testing.T) {
	cases := []struct {
		name string
		in   service.CreateTaskInput
	}{
		{"пустой заголовок", service.CreateTaskInput{Title: "   "}},
		{"длинный заголовок", service.CreateTaskInput{Title: string(make([]byte, 201))}},
		{"плохой статус", service.CreateTaskInput{Title: "x", Status: "archived"}},
		{"плохой приоритет", service.CreateTaskInput{Title: "x", Priority: "critical"}},
		{"слишком много меток", service.CreateTaskInput{Title: "x", Labels: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svcCreate(t, tc.in)
			if !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("ожидалась ErrInvalid, получено %v", err)
			}
		})
	}
}

func svcCreate(t *testing.T, in service.CreateTaskInput) (*domain.Task, error) {
	t.Helper()
	svc, _, _, _ := newService(t)
	return svc.Create(context.Background(), in, "test")
}

func TestUpdate_StatusChangePublishesBothEvents(t *testing.T) {
	svc, _, _, pub := newService(t)
	ctx := context.Background()

	task, err := svc.Create(ctx, service.CreateTaskInput{Title: "Задача", Status: domain.StatusTodo}, "test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newStatus := domain.StatusInProgress
	updated, err := svc.Update(ctx, task.ID, service.UpdateTaskInput{Status: &newStatus}, "Вы")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Status != domain.StatusInProgress {
		t.Fatalf("статус = %q", updated.Status)
	}

	types := pub.types()
	want := []string{service.EventTaskCreated, service.EventTaskStatusChanged, service.EventTaskUpdated}
	if !reflect.DeepEqual(types, want) {
		t.Errorf("события = %v, want %v", types, want)
	}

	var statusEvent *domain.Event
	for i := range pub.events {
		if pub.events[i].Type == service.EventTaskStatusChanged {
			statusEvent = &pub.events[i]
		}
	}
	if statusEvent == nil {
		t.Fatal("нет события task.status_changed")
	}
	if statusEvent.Payload["from"] != "todo" || statusEvent.Payload["to"] != "in_progress" {
		t.Errorf("payload from/to = %v/%v", statusEvent.Payload["from"], statusEvent.Payload["to"])
	}
}

func TestUpdate_NotFound(t *testing.T) {
	svc, _, _, _ := newService(t)
	title := "новое"
	_, err := svc.Update(context.Background(), "00000000-0000-0000-0000-000000000000",
		service.UpdateTaskInput{Title: &title}, "test")
	if !service.IsNotFound(err) {
		t.Fatalf("ожидался NotFound, получено %v", err)
	}
}

func TestMove_UpdatesStatusAndPosition(t *testing.T) {
	svc, _, _, _ := newService(t)
	ctx := context.Background()
	task, _ := svc.Create(ctx, service.CreateTaskInput{Title: "Перенос"}, "test")

	moved, err := svc.Move(ctx, task.ID, domain.StatusReview, 5000, "Вы")
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if moved.Status != domain.StatusReview || moved.Position != 5000 {
		t.Errorf("после Move: status=%q position=%v", moved.Status, moved.Position)
	}
}

func TestDelete(t *testing.T) {
	svc, repo, _, pub := newService(t)
	ctx := context.Background()
	task, _ := svc.Create(ctx, service.CreateTaskInput{Title: "На удаление"}, "test")

	if err := svc.Delete(ctx, task.ID, "Вы"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, task.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("задача не удалена: %v", err)
	}
	if got := pub.types(); !reflect.DeepEqual(got, []string{service.EventTaskCreated, service.EventTaskDeleted}) {
		t.Errorf("события = %v", got)
	}
	if err := svc.Delete(ctx, task.ID, "Вы"); !service.IsNotFound(err) {
		t.Errorf("повторное удаление должно дать NotFound, получено %v", err)
	}
}

func TestCreate_SequentialPositions(t *testing.T) {
	svc, _, _, _ := newService(t)
	ctx := context.Background()
	first, _ := svc.Create(ctx, service.CreateTaskInput{Title: "1"}, "t")
	second, _ := svc.Create(ctx, service.CreateTaskInput{Title: "2"}, "t")
	if second.Position <= first.Position {
		t.Errorf("позиции не возрастают: %v затем %v", first.Position, second.Position)
	}
}
