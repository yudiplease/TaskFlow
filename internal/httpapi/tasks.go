package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
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

// IntField — «обнуляемое» целое: различает «поле отсутствовало в JSON»
// (не менять), «null» (сбросить) и число. Обычный *int не может отличить
// null от отсутствия — а для «напомнить за N минут» нужны все три состояния.
// Значение (не указатель!) обязательно: на указатель декодер реагирует на
// null присвоением nil БЕЗ вызова UnmarshalJSON.
type IntField struct {
	Present bool
	IsNull  bool
	Value   int
}

func (f *IntField) UnmarshalJSON(b []byte) error {
	f.Present = true
	s := strings.TrimSpace(string(b))
	if s == "null" {
		f.IsNull = true
		return nil
	}
	return json.Unmarshal(b, &f.Value)
}

type createTaskRequest struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Priority    string   `json:"priority"`
	Assignee    string   `json:"assignee"`
	Labels      []string `json:"labels"`
	Color       string   `json:"color"`
	// Новый формат срока: due_at — RFC3339-инстант; due_all_day — «весь день»
	// (время скрыто, инстант = 23:59 локального дня); due_timezone — IANA.
	DueAt       *string `json:"due_at"`
	DueAllDay   bool    `json:"due_all_day"`
	DueTimezone string  `json:"due_timezone"`
	ReminderOffsetMinutes IntField `json:"reminder_offset_minutes"`
	// СТАРЫЙ формат (совместимость на переходный период): если клиент прислал
	// due_date и не прислал due_at — трактуем как due_at + due_all_day=true.
	DueDate *string `json:"due_date"`
	// SkipStickyUpdate=true — создание без явного действия пользователя
	// (шаблон, импорт, демо-данные): sticky-значения не запоминаем.
	SkipStickyUpdate bool `json:"skip_sticky_update"`
}

// dueRequest — разобранный срок из запроса.
type dueRequest struct {
	instant    *time.Time
	allDay     bool
	timezone   string
	reminder   *int
	reminderSet bool // reminder присутствовал в JSON (см. IntField)
	reminderNull bool
}

// parseDueRequest сводит новый (due_at) и старый (due_date) форматы к одному виду.
func parseDueRequest(dueAt, legacyDueDate *string, allDay bool, tz string, reminder *IntField) (dueRequest, error) {
	set := reminder != nil && reminder.Present
	out := dueRequest{allDay: allDay, timezone: tz, reminderSet: set, reminderNull: set && reminder.IsNull}
	if set && !reminder.IsNull {
		if reminder.Value < 0 {
			return out, errors.New("reminder_offset_minutes не может быть отрицательным")
		}
		out.reminder = &reminder.Value
	}
	src := dueAt
	if src == nil {
		src = legacyDueDate
		if src != nil && *src != "" {
			// Старый клиент присылал только дату (конец дня) — весь день.
			out.allDay = true
		}
	}
	if src == nil {
		return out, nil // срок в запросе не упоминался
	}
	if strings.TrimSpace(*src) == "" {
		return out, nil // пустая строка = «без срока»
	}
	t, err := time.Parse(time.RFC3339, *src)
	if err != nil {
		return out, errors.New("due_at должен быть в формате RFC3339")
	}
	out.instant = &t
	return out, nil
}

func (req dueRequest) applyToCreate(in *service.CreateTaskInput) error {
	in.DueAt = req.instant
	in.DueAllDay = req.allDay
	if req.timezone != "" {
		if _, err := time.LoadLocation(req.timezone); err != nil {
			return fmt.Errorf("неизвестный часовой пояс %q", req.timezone)
		}
		in.DueTimezone = req.timezone
	}
	if req.reminderSet {
		in.ReminderOffsetMinutes = req.reminder // nil = «без напоминания»
	}
	return nil
}

// stickyReminder возвращает значение для StickyPatch: nil = поле
// отсутствовало в запросе (не трогать), 0 = явный сброс («без напоминания»),
// >0 = запомнить.
func (d dueRequest) stickyReminder() *int {
	if !d.reminderSet {
		return nil
	}
	if d.reminderNull {
		zero := 0
		return &zero
	}
	v := 0
	if d.reminder != nil {
		v = *d.reminder
	}
	return &v
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req createTaskRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "некорректный JSON: "+err.Error())
		return
	}
	due, err := parseDueRequest(req.DueAt, req.DueDate, req.DueAllDay, req.DueTimezone, &req.ReminderOffsetMinutes)
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
		Color:       req.Color,
	}
	if err := due.applyToCreate(&in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	t, err := s.svc.Create(r.Context(), in, actorFrom(r))
	if err != nil {
		mapError(w, err)
		return
	}
	if !req.SkipStickyUpdate {
		s.applySticky(r, service.StickyPatch{
			Status:                &req.Status,
			Priority:              &req.Priority,
			Color:                 &req.Color,
			Assignee:              &req.Assignee,
			Labels:                &req.Labels,
			DueTimeOfDay:          dueTimeOfDayPatch(due),
			ReminderOffsetMinutes: due.stickyReminder(),
		})
	}
	writeJSON(w, http.StatusCreated, t)
}

// dueTimeOfDayPatch извлекает «ЧЧ:ММ» из срока запроса в его же TZ —
// для sticky-запоминания времени (но не даты). All-day или отсутствие
// срока сбрасывает запомненное время.
func dueTimeOfDayPatch(due dueRequest) *string {
	if due.instant == nil || due.allDay {
		empty := ""
		return &empty
	}
	loc := time.Local
	if due.timezone != "" {
		if l, err := time.LoadLocation(due.timezone); err == nil {
			loc = l
		}
	}
	t := due.instant.In(loc)
	tod := fmt.Sprintf("%02d:%02d", t.Hour(), t.Minute())
	return &tod
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
	if v := q.Get("color"); v != "" {
		color, err := domain.NormalizeColor(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		f.Color = color
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
	Color       *string   `json:"color"`
	// Срок: due_at (RFC3339; "" = убрать срок), due_all_day, due_timezone.
	// reminder_offset_minutes — tri-state: отсутствовало = не менять,
	// null = убрать напоминание, число = установить.
	DueAt       *string   `json:"due_at"`
	DueAllDay   *bool     `json:"due_all_day"`
	DueTimezone *string   `json:"due_timezone"`
	ReminderOffsetMinutes IntField `json:"reminder_offset_minutes"`
	// СТАРЫЙ формат (совместимость): если due_at не прислан — due_date
	// трактуется как due_at + due_all_day=true ("" = убрать срок).
	DueDate *string  `json:"due_date"`
	Position *float64 `json:"position"`
	// SkipStickyUpdate=true — правка без явного выбора значений пользователем
	// (скрипты, импорт): sticky-значения не запоминаем.
	SkipStickyUpdate bool `json:"skip_sticky_update"`
}

func (s *Server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req updateTaskRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "некорректный JSON: "+err.Error())
		return
	}

	due, err := parseDueRequest(req.DueAt, req.DueDate, req.DueAllDay != nil && *req.DueAllDay, derefString(req.DueTimezone), &req.ReminderOffsetMinutes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.DueTimezone != nil && *req.DueTimezone != "" {
		if _, err := time.LoadLocation(*req.DueTimezone); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("неизвестный часовой пояс %q", *req.DueTimezone))
			return
		}
	}

	in := service.UpdateTaskInput{
		Title:       req.Title,
		Description: req.Description,
		Assignee:    req.Assignee,
		Labels:      req.Labels,
		Color:       req.Color,
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
	// Срок: due_at="" (или legacy due_date="") = ClearDue; иначе инстант.
	if due.instant != nil {
		in.DueAt = due.instant
	} else if req.DueAt != nil || req.DueDate != nil {
		in.ClearDue = true
	}
	if req.DueAllDay != nil {
		in.DueAllDay = req.DueAllDay
	}
	if req.DueTimezone != nil {
		in.DueTimezone = req.DueTimezone
	}
	if due.reminderSet {
		in.ReminderOffset = due.reminder
		in.ClearReminder = due.reminderNull
	}

	t, err := s.svc.Update(r.Context(), id, in, actorFrom(r))
	if err != nil {
		mapError(w, err)
		return
	}
	// Sticky обновляют только поля, реально присутствовавшие в PATCH:
	// точечные правки (позиция) не затирают остальные значения.
	if !req.SkipStickyUpdate {
		patch := service.StickyPatch{
			Status:   req.Status,
			Priority: req.Priority,
			Color:    req.Color,
			Assignee: req.Assignee,
			Labels:   req.Labels,
		}
		// Время суток запоминаем только если срок был в запросе; all-day
		// или сброс срока сбрасывают запомненное время.
		if req.DueAt != nil || req.DueDate != nil {
			patch.DueTimeOfDay = dueTimeOfDayPatch(due)
		}
		patch.ReminderOffsetMinutes = due.stickyReminder()
		s.applySticky(r, patch)
	}
	writeJSON(w, http.StatusOK, t)
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
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

type colorTaskRequest struct {
	Color string `json:"color"`
}

// handleColorTask — PATCH /tasks/{id}/color: отдельная смена цветовой метки.
// Пустая строка снимает цвет.
func (s *Server) handleColorTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req colorTaskRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "некорректный JSON: "+err.Error())
		return
	}
	t, err := s.svc.Update(r.Context(), id, service.UpdateTaskInput{Color: &req.Color}, actorFrom(r))
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// handleColors — GET /api/colors: фиксированный справочник цветовых меток.
// Единственный источник правды для палитры и легенды на фронтенде.
func (s *Server) handleColors(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"colors": domain.ColorTags})
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
