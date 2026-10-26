package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"tasktracker/internal/config"
	"tasktracker/internal/domain"
	"tasktracker/internal/events"
	"tasktracker/internal/httpapi"
	"tasktracker/internal/metrics"
	"tasktracker/internal/service"
	"tasktracker/internal/storage"
)

// memDefaults — память-репозиторий «залипающих» значений для сквозных тестов.
type memDefaults struct {
	mu    sync.Mutex
	items map[string]domain.TaskDefaults
}

func newMemDefaults() *memDefaults { return &memDefaults{items: map[string]domain.TaskDefaults{}} }

func (m *memDefaults) Get(_ context.Context, actor string) (domain.TaskDefaults, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.items[actor]
	if !ok {
		return domain.TaskDefaults{}, time.Time{}, storage.ErrNotFound
	}
	return d, time.Now().UTC(), nil
}

func (m *memDefaults) Put(_ context.Context, actor string, d domain.TaskDefaults) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[actor] = d
	return time.Now().UTC(), nil
}

func newDefaultsTestServer(t *testing.T) (*httptest.Server, *memDefaults) {
	t.Helper()
	repo := newMemRepo()
	defRepo := newMemDefaults()
	svc := service.NewTaskService(repo, memActivity{}, events.NoopPublisher{}, metrics.New())
	api := httpapi.NewServer(httpapi.Deps{
		Config:        config.Load(),
		Service:       svc,
		Notifications: service.NewNotifications(&memNotifications{}),
		Defaults:      service.NewDefaultsService(defRepo),
		Metrics:       metrics.New(),
		PingPostgres:  func() error { return nil },
	})
	return httptest.NewServer(api.Handler()), defRepo
}

func TestSticky_CreateTaskRemembersFields(t *testing.T) {
	srv, defRepo := newDefaultsTestServer(t)
	defer srv.Close()

	res, body := doJSON(t, srv, "POST", "/api/tasks", map[string]any{
		"title":    "Починить CI",
		"status":   "in_progress",
		"priority": "urgent",
		"assignee": "Анна",
		"labels":   []string{"devops"},
		"color":    "#e53935",
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("POST = %d, %v", res.StatusCode, body)
	}

	d := defRepo.items[service.DefaultActor]
	if d.Status != "in_progress" || d.Priority != "urgent" || d.Assignee != "Анна" {
		t.Errorf("sticky-значения после создания = %+v", d)
	}
	if len(d.Labels) != 1 || d.Labels[0] != "devops" {
		t.Errorf("метки = %v, want [devops]", d.Labels)
	}
	if d.Color != "#E53935" {
		t.Errorf("цвет = %q, want #E53935", d.Color)
	}
}

func TestSticky_PatchUpdatesOnlyPresentFields(t *testing.T) {
	srv, defRepo := newDefaultsTestServer(t)
	defer srv.Close()

	_, created := doJSON(t, srv, "POST", "/api/tasks", map[string]any{
		"title": "База", "status": "review", "priority": "high", "assignee": "Михаил",
	})
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("пустой id в ответе")
	}

	// PATCH меняет только приоритет — остальные sticky-поля сохраняются.
	res, body := doJSON(t, srv, "PATCH", "/api/tasks/"+id, map[string]any{"priority": "low"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PATCH = %d, %v", res.StatusCode, body)
	}
	d := defRepo.items[service.DefaultActor]
	if d.Priority != "low" {
		t.Errorf("приоритет = %q, want low", d.Priority)
	}
	if d.Status != "review" || d.Assignee != "Михаил" {
		t.Errorf("поля вне PATCH перетёрлись: %+v", d)
	}
}

func TestSticky_TaskBoundFieldsNotRemembered(t *testing.T) {
	srv, defRepo := newDefaultsTestServer(t)
	defer srv.Close()

	res, _ := doJSON(t, srv, "POST", "/api/tasks", map[string]any{
		"title":       "С заголовком и сроком",
		"description": "конкретная задача",
		"due_date":    "2030-01-01T23:59:00Z",
		"priority":    "urgent",
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("POST = %d", res.StatusCode)
	}
	d := defRepo.items[service.DefaultActor]
	// TaskDefaults структурно не содержит title/description/due_date:
	// проверяем, что в сохранённом JSON нет ни одного такого ключа.
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"title", "description", "due_date", "id"} {
		if _, ok := stored[forbidden]; ok {
			t.Errorf("поле %q не должно запоминаться (привязано к задаче): %v", forbidden, stored)
		}
	}
	if stored["priority"] != "urgent" {
		t.Errorf("приоритет должен запомниться: %v", stored)
	}
}

func TestSticky_SkipFlagPreventsUpdate(t *testing.T) {
	srv, defRepo := newDefaultsTestServer(t)
	defer srv.Close()

	res, body := doJSON(t, srv, "POST", "/api/tasks", map[string]any{
		"title":              "Импорт без действия пользователя",
		"status":             "done",
		"priority":           "urgent",
		"skip_sticky_update": true,
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("POST = %d, %v", res.StatusCode, body)
	}
	if d := defRepo.items[service.DefaultActor]; !d.Empty() {
		t.Errorf("skip_sticky_update должен предотвратить запоминание, получено %+v", d)
	}
}

func TestSticky_MoveDoesNotAffectDefaults(t *testing.T) {
	srv, defRepo := newDefaultsTestServer(t)
	defer srv.Close()

	_, created := doJSON(t, srv, "POST", "/api/tasks", map[string]any{
		"title": "Переносимая", "status": "todo", "priority": "medium",
	})
	id, _ := created["id"].(string)

	res, _ := doJSON(t, srv, "POST", "/api/tasks/"+id+"/move", map[string]any{"status": "done", "position": 100})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("move = %d", res.StatusCode)
	}
	d := defRepo.items[service.DefaultActor]
	// Drag&drop — не выбор значений в форме: статус не должен «залипать».
	if d.Status != "todo" {
		t.Errorf("после move sticky-статус = %q, want todo", d.Status)
	}
}

func TestSticky_DueDateOnlyPatchIsNoop(t *testing.T) {
	srv, defRepo := newDefaultsTestServer(t)
	defer srv.Close()

	_, created := doJSON(t, srv, "POST", "/api/tasks", map[string]any{
		"title": "Со сроком", "priority": "high",
	})
	id, _ := created["id"].(string)

	res, _ := doJSON(t, srv, "PATCH", "/api/tasks/"+id, map[string]any{"due_date": "2030-05-01T23:59:00Z"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PATCH срока = %d", res.StatusCode)
	}
	d := defRepo.items[service.DefaultActor]
	if d.Priority != "high" {
		t.Errorf("PATCH одного срока не должен менять приоритет: %+v", d)
	}
}

func TestDefaultsAPI_PutAndGet(t *testing.T) {
	srv, _ := newDefaultsTestServer(t)
	defer srv.Close()

	res, body := doJSON(t, srv, "PUT", "/api/defaults", map[string]any{
		"status": "review", "priority": "urgent", "labels": []string{"api"},
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PUT defaults = %d, %v", res.StatusCode, body)
	}
	if ts, _ := body["updated_at"].(string); ts == "" {
		t.Errorf("PUT должен вернуть updated_at: %v", body)
	}

	res, body = doJSON(t, srv, "GET", "/api/defaults", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET defaults = %d", res.StatusCode)
	}
	d, _ := body["defaults"].(map[string]any)
	if d["status"] != "review" || d["priority"] != "urgent" {
		t.Errorf("GET вернул %+v", d)
	}

	// Невалидные значения отклоняются.
	res, _ = doJSON(t, srv, "PUT", "/api/defaults", map[string]any{"status": "archived"})
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("PUT невалидного статуса = %d, want 400", res.StatusCode)
	}

	// Пустой объект — легальный полный сброс.
	res, _ = doJSON(t, srv, "PUT", "/api/defaults", map[string]any{})
	if res.StatusCode != http.StatusOK {
		t.Errorf("PUT пустого объекта = %d, want 200", res.StatusCode)
	}
	res, body = doJSON(t, srv, "GET", "/api/defaults", nil)
	d, _ = body["defaults"].(map[string]any)
	if len(d) != 0 {
		t.Errorf("после сброса defaults = %v, want пусто", d)
	}
}

func TestSticky_PerUserIsolation(t *testing.T) {
	srv, defRepo := newDefaultsTestServer(t)
	defer srv.Close()

	// doJSON не ставит X-Actor → актор system.
	doJSON(t, srv, "POST", "/api/tasks", map[string]any{"title": "А", "priority": "urgent"})

	// Задача другого актора (заголовок с кириллицей — как шлёт фронтенд).
	req, _ := http.NewRequest("POST", srv.URL+"/api/tasks",
		strings.NewReader(`{"title":"Б","priority":"low"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Actor", "%D0%90%D0%BD%D0%BD%D0%B0") // encodeURIComponent("Анна")
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("POST с X-Actor = %d", res.StatusCode)
	}

	system := defRepo.items[service.DefaultActor]
	anna := defRepo.items["Анна"]
	if system.Priority != "urgent" || anna.Priority != "low" {
		t.Errorf("значения перемешались между пользователями: system=%+v anna=%+v", system, anna)
	}
}
