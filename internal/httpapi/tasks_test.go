package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
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

// Хранилище в памяти, достаточное для сквозных тестов HTTP.
type memRepo struct {
	mu    sync.Mutex
	tasks map[string]*domain.Task
	max   map[domain.Status]float64
	next  int
}

func newMemRepo() *memRepo {
	return &memRepo{tasks: map[string]*domain.Task{}, max: map[domain.Status]float64{}}
}

func (m *memRepo) Create(_ context.Context, t *domain.Task) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tasks[t.ID] = t
	if t.Position > m.max[t.Status] {
		m.max[t.Status] = t.Position
	}
	return nil
}

func (m *memRepo) Get(_ context.Context, id string) (*domain.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tasks[id]; ok {
		cp := *t
		return &cp, nil
	}
	return nil, storage.ErrNotFound
}

func (m *memRepo) Update(_ context.Context, t *domain.Task) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tasks[t.ID]; !ok {
		return storage.ErrNotFound
	}
	m.tasks[t.ID] = t
	return nil
}

func (m *memRepo) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tasks[id]; !ok {
		return storage.ErrNotFound
	}
	delete(m.tasks, id)
	return nil
}

func (m *memRepo) List(_ context.Context, f domain.TaskFilter) ([]domain.Task, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Task
	for _, t := range m.tasks {
		if f.Status != nil && t.Status != *f.Status {
			continue
		}
		if f.Search != "" && !strings.Contains(strings.ToLower(t.Title), strings.ToLower(f.Search)) {
			continue
		}
		cp := *t
		out = append(out, cp)
	}
	return out, len(out), nil
}

func (m *memRepo) MaxPosition(_ context.Context, st domain.Status) (float64, error) {
	return m.max[st], nil
}

func (m *memRepo) Stats(_ context.Context) (*domain.Stats, error) {
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

func (m *memRepo) ListDue(_ context.Context, notAfter time.Time) ([]domain.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Task
	for _, t := range m.tasks {
		if t.Status != domain.StatusDone && t.DueAt != nil && !t.DueAt.After(notAfter) {
			out = append(out, *t)
		}
	}
	return out, nil
}

type memNotifications struct {
	mu    sync.Mutex
	items []domain.Notification
}

func (m *memNotifications) Insert(_ context.Context, n *domain.Notification) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := n.TaskID + ":" + n.Type + ":" + n.CreatedAt.Format("2006-01-02")
	for _, ex := range m.items {
		if ex.TaskID+":"+ex.Type+":"+ex.CreatedAt.Format("2006-01-02") == key {
			return false, nil
		}
	}
	n.ID = int64(len(m.items) + 1)
	m.items = append(m.items, *n)
	return true, nil
}

func (m *memNotifications) List(_ context.Context, onlyUnread bool, limit int) ([]domain.Notification, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []domain.Notification{}
	for i := len(m.items) - 1; i >= 0 && len(out) < limit; i-- {
		if onlyUnread && m.items[i].Read {
			continue
		}
		out = append(out, m.items[i])
	}
	return out, nil
}

func (m *memNotifications) MarkRead(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.items {
		if m.items[i].ID == id {
			m.items[i].Read = true
		}
	}
	return nil
}

func (m *memNotifications) MarkAllRead(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.items {
		m.items[i].Read = true
	}
	return nil
}

func (m *memNotifications) CountUnread(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, it := range m.items {
		if !it.Read {
			n++
		}
	}
	return n, nil
}

type memActivity struct{}

func (memActivity) Insert(context.Context, *domain.Activity) error { return nil }
func (memActivity) ListByTask(context.Context, string, int) ([]domain.Activity, error) {
	return []domain.Activity{}, nil
}
func (memActivity) ListRecent(context.Context, int) ([]domain.Activity, error) {
	return []domain.Activity{}, nil
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	repo := newMemRepo()
	notifRepo := &memNotifications{}
	svc := service.NewTaskService(repo, memActivity{}, events.NoopPublisher{}, metrics.New())
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := httpapi.NewServer(httpapi.Deps{
		Config:        config.Load(),
		Service:       svc,
		Notifications: service.NewNotifications(notifRepo),
		Metrics:       metrics.New(),
		Log:           log,
		PingPostgres:  func() error { return nil },
	})
	return httptest.NewServer(api.Handler())
}

func doJSON(t *testing.T, srv *httptest.Server, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req, _ := http.NewRequest(method, srv.URL+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res, out
}

func TestCRUDFlow(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	// Создание
	res, body := doJSON(t, srv, "POST", "/api/tasks", map[string]any{
		"title":    "Интеграционная задача",
		"priority": "high",
		"labels":   []string{"api", "тест"},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", res.StatusCode, body)
	}
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatal("пустой id в ответе")
	}
	if body["status"] != "backlog" || body["priority"] != "high" {
		t.Errorf("поля по умолчанию: %v", body)
	}

	// Список
	res, listBody := doJSON(t, srv, "GET", "/api/tasks", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET list: %d", res.StatusCode)
	}
	if total, _ := listBody["total"].(float64); total != 1 {
		t.Errorf("total = %v, want 1", listBody["total"])
	}
	tasks, _ := listBody["tasks"].([]any)
	if len(tasks) != 1 {
		t.Errorf("задач в списке: %d", len(tasks))
	}

	// Обновление
	res, body = doJSON(t, srv, "PATCH", "/api/tasks/"+id, map[string]any{"status": "in_progress"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PATCH: %d, %v", res.StatusCode, body)
	}
	if body["status"] != "in_progress" {
		t.Errorf("после PATCH status = %v", body["status"])
	}

	// Перенос
	res, body = doJSON(t, srv, "POST", "/api/tasks/"+id+"/move", map[string]any{"status": "done", "position": 4096})
	if res.StatusCode != http.StatusOK || body["status"] != "done" {
		t.Errorf("move: %d %v", res.StatusCode, body)
	}

	// Удаление
	res, _ = doJSON(t, srv, "DELETE", "/api/tasks/"+id, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Errorf("DELETE: %d", res.StatusCode)
	}

	// Удалённая не находится
	res, _ = doJSON(t, srv, "GET", "/api/tasks/"+id, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("GET после удаления: %d, want 404", res.StatusCode)
	}
}

func TestCreateValidation_Returns400(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	res, body := doJSON(t, srv, "POST", "/api/tasks", map[string]any{"title": ""})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body %v", res.StatusCode, body)
	}
	if msg, _ := body["error"].(string); msg == "" {
		t.Error("пустое сообщение об ошибке")
	}
}

func TestInvalidJSON_Returns400(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/api/tasks", strings.NewReader("{не json"))
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", res.StatusCode)
	}
}

func TestListFiltersByStatus(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	doJSON(t, srv, "POST", "/api/tasks", map[string]any{"title": "в бэклоге"})
	doJSON(t, srv, "POST", "/api/tasks", map[string]any{"title": "в работе", "status": "in_progress"})

	res, body := doJSON(t, srv, "GET", "/api/tasks?status=in_progress", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	tasks, _ := body["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("отфильтровано задач: %d, want 1", len(tasks))
	}
	first := tasks[0].(map[string]any)
	if first["title"] != "в работе" {
		t.Errorf("вернулась не та задача: %v", first["title"])
	}
}

func TestHealthEndpoints(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	res, _ := doJSON(t, srv, "GET", "/healthz", nil)
	if res.StatusCode != http.StatusOK {
		t.Errorf("healthz = %d", res.StatusCode)
	}

	res, body := doJSON(t, srv, "GET", "/readyz", nil)
	if res.StatusCode != http.StatusOK {
		t.Errorf("readyz = %d, body %v", res.StatusCode, body)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	doJSON(t, srv, "GET", "/api/stats", nil)

	res, err := srv.Client().Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("metrics = %d", res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, metric := range []string{"http_requests_total", "http_request_duration_seconds", "tasks_by_status"} {
		if !strings.Contains(string(body), metric) {
			t.Errorf("метрика %s отсутствует в /metrics", metric)
		}
	}
}

func TestNotificationsAPI(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	// Создать задачу со сроком «сегодня» и запустить нотификатор напрямую
	// невозможно через HTTP — проверяем API-контур на пустом состоянии,
	// затем имитируем напоминание через планировщик не нужно: маршрут
	// должен корректно отдавать пустой список иunread=0.
	res, body := doJSON(t, srv, "GET", "/api/notifications", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET notifications = %d", res.StatusCode)
	}
	if unread, _ := body["unread"].(float64); unread != 0 {
		t.Errorf("unread = %v, want 0", unread)
	}
	if items, _ := body["notifications"].([]any); len(items) != 0 {
		t.Errorf("ожидался пустой список, получено %d", len(items))
	}

	res, _ = doJSON(t, srv, "POST", "/api/notifications/read-all", nil)
	if res.StatusCode != http.StatusNoContent {
		t.Errorf("read-all = %d, want 204", res.StatusCode)
	}

	res, _ = doJSON(t, srv, "POST", "/api/notifications/abc/read", nil)
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("read с некорректным id = %d, want 400", res.StatusCode)
	}
}

func TestSearchFilter(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	doJSON(t, srv, "POST", "/api/tasks", map[string]any{"title": "Kafka pipeline"})
	doJSON(t, srv, "POST", "/api/tasks", map[string]any{"title": "Postgres миграции"})

	res, body := doJSON(t, srv, "GET", "/api/tasks?q=kafka", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	tasks, _ := body["tasks"].([]any)
	if len(tasks) != 1 || tasks[0].(map[string]any)["title"] != "Kafka pipeline" {
		t.Errorf("поиск «kafka» вернул: %v", tasks)
	}
}

func (m *memRepo) ListRemindersDue(_ context.Context, now time.Time) ([]domain.Task, error) {
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

func (m *memRepo) MarkReminderSent(_ context.Context, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return storage.ErrNotFound
	}
	if t.ReminderSentAt == nil {
		t.ReminderSentAt = &at
	}
	return nil
}
