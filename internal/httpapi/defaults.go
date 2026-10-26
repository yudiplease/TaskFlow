package httpapi

import (
	"net/http"

	"tasktracker/internal/domain"
	"tasktracker/internal/service"
)

// handleGetDefaults — GET /api/defaults: «залипающие» значения формы
// новой задачи для текущего пользователя (актор из X-Actor).
func (s *Server) handleGetDefaults(w http.ResponseWriter, r *http.Request) {
	if s.defaults == nil {
		writeError(w, http.StatusServiceUnavailable, "defaults недоступны")
		return
	}
	d, updatedAt, err := s.defaults.Get(r.Context(), actorFrom(r))
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"defaults":   d,
		"updated_at": updatedAt,
	})
}

// handlePutDefaults — PUT /api/defaults: сохранить значения.
// Вызывается фронтом после успешного сохранения задачи и при сбросе.
func (s *Server) handlePutDefaults(w http.ResponseWriter, r *http.Request) {
	if s.defaults == nil {
		writeError(w, http.StatusServiceUnavailable, "defaults недоступны")
		return
	}
	var req domain.TaskDefaults
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "некорректный JSON: "+err.Error())
		return
	}
	d, updatedAt, err := s.defaults.Put(r.Context(), actorFrom(r), req)
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"defaults":   d,
		"updated_at": updatedAt,
	})
}

// applySticky обновляет «залипающие» значения пользователя после успешного
// сохранения задачи. Побочный эффект: ошибка не должна ломать уже выполненное
// сохранение — только запись в журнал.
func (s *Server) applySticky(r *http.Request, p service.StickyPatch) {
	if s.defaults == nil || p.Empty() {
		return
	}
	actor := actorFrom(r)
	if _, _, err := s.defaults.ApplyPatch(r.Context(), actor, p); err != nil {
		s.log.Warn("не удалось обновить значения по умолчанию", "err", err, "actor", actor)
	}
}
