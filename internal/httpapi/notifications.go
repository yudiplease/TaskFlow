package httpapi

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// handleNotifications возвращает последние напоминания и число непрочитанных.
func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	limit := intQuery(r, "limit", 30)
	res, err := s.notifications.List(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "не удалось загрузить напоминания")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleNotificationRead отмечает напоминание прочитанным.
func (s *Server) handleNotificationRead(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "некорректный id напоминания")
		return
	}
	if err := s.notifications.MarkRead(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "не удалось обновить напоминание")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleNotificationsReadAll отмечает все напоминания прочитанными.
func (s *Server) handleNotificationsReadAll(w http.ResponseWriter, r *http.Request) {
	if err := s.notifications.MarkAllRead(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "не удалось обновить напоминания")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
