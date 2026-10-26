package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"tasktracker/internal/domain"
	"tasktracker/internal/service"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func actorFrom(r *http.Request) string {
	a := strings.TrimSpace(r.Header.Get("X-Actor"))
	// Клиент кодирует кириллицу (fetch не принимает не-ISO-8859-1 заголовки).
	if strings.Contains(a, "%") {
		if decoded, err := url.QueryUnescape(a); err == nil {
			a = decoded
		}
	}
	if a == "" || !utf8.ValidString(a) {
		return service.DefaultActor
	}
	return a
}

// mapError переводит ошибки домена в HTTP-коды.
func mapError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case service.IsNotFound(err):
		writeError(w, http.StatusNotFound, "задача не найдена")
	default:
		writeError(w, http.StatusInternalServerError, "внутренняя ошибка сервера")
	}
}

func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return err
	}
	return nil
}

type createTaskRequest struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Priority    string   `json:"priority"`
	Assignee    string   `json:"assignee"`
	Labels      []string `json:"labels"`
	DueDate     *string  `json:"due_date"`
}

func parseTime(s *string) (*time.Time, error) {
	if s == nil {
		return nil, nil
	}
	if strings.TrimSpace(*s) == "" {
		return nil, nil // пустая строка = без срока (сброс для PATCH обрабатывается отдельно)
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return nil, errors.New("due_date должен быть в формате RFC3339")
	}
	return &t, nil
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req createTaskRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "некорректный JSON: "+err.Error())
		return
	}
	due, err := parseTime(req.DueDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	in := service.CreateTaskInput{
		Title:       req.Title,
		Description: req.Description,
		Status:      domain.Status(req.Status),
		Priority:    domain.Priority(req.Priority),
		Assignee:    req.Assignee,
		Labels:      req.Labels,
		DueDate:     due,
	}
	t, err := s.svc.Create(r.Context(), in, actorFrom(r))
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := domain.TaskFilter{
		Assignee: q.Get("assignee"),
		Label:    q.Get("label"),
		Search:   q.Get("q"),
	}
	if v := q.Get("status"); v != "" {
		st := domain.Status(v)
		f.Status = &st
	}
	if v := q.Get("priority"); v != "" {
		p := domain.Priority(v)
		f.Priority = &p
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Offset = n
		}
	}

	tasks, total, err := s.svc.List(r.Context(), f)
	if err != nil {
		mapError(w, err)
		return
	}
	if tasks == nil {
		tasks = []domain.Task{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks, "total": total})
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, err := s.svc.Get(r.Context(), id)
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

type updateTaskRequest struct {
	Title       *string   `json:"title"`
	Description *string   `json:"description"`
	Status      *string   `json:"status"`
	Priority    *string   `json:"priority"`
	Assignee    *string   `json:"assignee"`
	Labels      *[]string `json:"labels"`
	DueDate     *string   `json:"due_date"`
	Position    *float64  `json:"position"`
}

func (s *Server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req updateTaskRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "некорректный JSON: "+err.Error())
		return
	}

	in := service.UpdateTaskInput{
		Title:       req.Title,
		Description: req.Description,
		Assignee:    req.Assignee,
		Labels:      req.Labels,
		Position:    req.Position,
	}
	if req.Status != nil {
		st := domain.Status(*req.Status)
		in.Status = &st
	}
	if req.Priority != nil {
		p := domain.Priority(*req.Priority)
		in.Priority = &p
	}
	if req.DueDate != nil {
		if *req.DueDate == "" {
			in.ClearDueDate = true
		} else {
			due, err := parseTime(req.DueDate)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			in.DueDate = due
		}
	}

	t, err := s.svc.Update(r.Context(), id, in, actorFrom(r))
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

type moveTaskRequest struct {
	Status   string   `json:"status"`
	Position *float64 `json:"position"`
}

func (s *Server) handleMoveTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req moveTaskRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "некорректный JSON: "+err.Error())
		return
	}
	pos := 0.0
	if req.Position != nil {
		pos = *req.Position
	}
	t, err := s.svc.Move(r.Context(), id, domain.Status(req.Status), pos, actorFrom(r))
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.svc.Delete(r.Context(), id, actorFrom(r)); err != nil {
		mapError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTaskActivity(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	limit := intQuery(r, "limit", 50)
	acts, err := s.svc.Activity(r.Context(), id, limit)
	if err != nil {
		mapError(w, err)
		return
	}
	if acts == nil {
		acts = []domain.Activity{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"activity": acts})
}

func (s *Server) handleRecentActivity(w http.ResponseWriter, r *http.Request) {
	limit := intQuery(r, "limit", 30)
	acts, err := s.svc.RecentActivity(r.Context(), limit)
	if err != nil {
		mapError(w, err)
		return
	}
	if acts == nil {
		acts = []domain.Activity{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"activity": acts})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	st, err := s.svc.Stats(r.Context())
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func intQuery(r *http.Request, key string, def int) int {
	if v := r.URL.Query().Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
