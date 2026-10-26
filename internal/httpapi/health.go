package httpapi

import (
	"net/http"
)

// handleLiveness — проба живости: процесс отвечает.
func (s *Server) handleLiveness(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadiness — готовность принимать трафик. Хранилище обязательно,
// деградация Kafka не блокирует работу (события копятся в очереди).
func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{"status": "ok", "mode": s.mode}
	code := http.StatusOK

	if s.pingFn != nil {
		if err := s.pingFn(); err != nil {
			resp["status"] = "degraded"
			resp["storage"] = "unavailable"
			resp["error"] = err.Error()
			code = http.StatusServiceUnavailable
			writeJSON(w, code, resp)
			return
		}
		resp["storage"] = "ok"
	}
	if s.kafkaOK != nil {
		if s.kafkaOK() {
			resp["kafka"] = "ok"
		} else {
			resp["kafka"] = "degraded"
		}
	}
	writeJSON(w, code, resp)
}
