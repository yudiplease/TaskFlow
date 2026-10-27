package httpapi

import (
	"errors"
	"net/http"

	"tasktracker/internal/updatechecker"
)

// Проверка обновлений (MVP): состояние для баннера/настроек, принудительная
// проверка и сохранение настроек. Прокси к GitHub нет — всё через кэш
// CachedChecker, сетевые запросы делает он сам.

// handleUpdatesState отдаёт снимок состояния: настройки, кэш релиза,
// признак «доступна новая версия» (считает сервер, UI только рисует).
func (s *Server) handleUpdatesState(w http.ResponseWriter, r *http.Request) {
	if s.updates == nil {
		// Режим без проверки обновлений (например, веб-версия в Docker):
		// фронтенд по 503 скрывает секцию и баннер.
		writeError(w, http.StatusServiceUnavailable, "проверка обновлений недоступна")
		return
	}
	st, err := s.updates.Status(r.Context(), s.appVersion)
	if err != nil {
		s.log.Error("состояние обновлений", "err", err)
		writeError(w, http.StatusInternalServerError, "не удалось прочитать состояние обновлений")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// handleUpdatesCheck — кнопка «Проверить сейчас»: принудительный чек минуя TTL.
// Ошибка (в т.ч. rate-limit) — единственное видимое пользователю сообщение:
// 502 → тост «Не удалось проверить обновления».
func (s *Server) handleUpdatesCheck(w http.ResponseWriter, r *http.Request) {
	if s.updates == nil {
		writeError(w, http.StatusServiceUnavailable, "проверка обновлений недоступна")
		return
	}
	if _, err := s.updates.CheckNow(r.Context()); err != nil {
		s.log.Warn("ручная проверка обновлений", "err", err)
		writeError(w, http.StatusBadGateway, "Не удалось проверить обновления")
		return
	}
	st, err := s.updates.Status(r.Context(), s.appVersion)
	if err != nil {
		s.log.Error("состояние обновлений после проверки", "err", err)
		writeError(w, http.StatusInternalServerError, "не удалось прочитать состояние обновлений")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// handleUpdatesSettings сохраняет чекбокс «Автоматически проверять» и интервал.
func (s *Server) handleUpdatesSettings(w http.ResponseWriter, r *http.Request) {
	if s.updates == nil {
		writeError(w, http.StatusServiceUnavailable, "проверка обновлений недоступна")
		return
	}
	var req struct {
		Enabled       bool `json:"enabled"`
		IntervalHours int  `json:"interval_hours"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "некорректное тело запроса")
		return
	}
	// Интервал валидируется и в CachedChecker; здесь — ранняя проверка для 400.
	if req.IntervalHours != 24 && req.IntervalHours != 72 && req.IntervalHours != 168 {
		writeError(w, http.StatusBadRequest, "интервал может быть 24, 72 или 168 часов")
		return
	}
	if err := s.updates.SetSettings(r.Context(), req.Enabled, req.IntervalHours); err != nil {
		if errors.Is(err, updatechecker.ErrNotFound) {
			writeError(w, http.StatusInternalServerError, "состояние обновлений не инициализировано")
			return
		}
		s.log.Error("сохранение настроек обновлений", "err", err)
		writeError(w, http.StatusInternalServerError, "не удалось сохранить настройки обновлений")
		return
	}
	st, err := s.updates.Status(r.Context(), s.appVersion)
	if err != nil {
		s.log.Error("состояние обновлений после сохранения", "err", err)
		writeError(w, http.StatusInternalServerError, "не удалось прочитать состояние обновлений")
		return
	}
	writeJSON(w, http.StatusOK, st)
}
