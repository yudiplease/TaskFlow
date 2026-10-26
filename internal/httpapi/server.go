// Package httpapi — REST API и раздача веб-интерфейса.
package httpapi

import (
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"tasktracker/internal/config"
	"tasktracker/internal/metrics"
	"tasktracker/internal/service"
	"tasktracker/web"
)

// Server — HTTP-сервер приложения.
type Server struct {
	cfg           config.Config
	svc           *service.TaskService
	notifications *service.Notifications
	defaults      *service.DefaultsService
	metrics       *metrics.Metrics
	log           *slog.Logger
	pingFn        func() error
	kafkaOK       func() bool
	mode          string
}

// Deps — зависимости сервера. pingFn проверяет PostgreSQL,
// kafkaOK (необязательно) сообщает о состоянии Kafka,
// Mode подписывает режим: "server" (полный стек) или "desktop" (SQLite).
type Deps struct {
	Config        config.Config
	Service       *service.TaskService
	Notifications *service.Notifications
	Defaults      *service.DefaultsService
	Metrics       *metrics.Metrics
	Log           *slog.Logger
	PingPostgres  func() error
	KafkaHealthy  func() bool
	Mode          string
}

// NewServer собирает сервер. Отсутствующий логгер заменяется стандартным.
func NewServer(d Deps) *Server {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	mode := d.Mode
	if mode == "" {
		mode = "server"
	}
	return &Server{
		cfg:           d.Config,
		svc:           d.Service,
		notifications: d.Notifications,
		defaults:      d.Defaults,
		metrics:       d.Metrics,
		log:           log,
		pingFn:        d.PingPostgres,
		kafkaOK:       d.KafkaHealthy,
		mode:          mode,
	}
}

// Handler собирает все маршруты с middleware.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(s.metricsMiddleware)
	r.Use(s.recoverMiddleware)
	r.Use(s.logMiddleware)
	r.Use(corsMiddleware)

	r.Get("/healthz", s.handleLiveness)
	r.Get("/readyz", s.handleReadiness)
	r.Handle("/metrics", promhttp.Handler())

	r.Route("/api", func(r chi.Router) {
		r.Get("/stats", s.handleStats)
		r.Get("/activity", s.handleRecentActivity)
		r.Get("/colors", s.handleColors)
		r.Get("/defaults", s.handleGetDefaults)
		r.Put("/defaults", s.handlePutDefaults)
		r.Get("/defaults", s.handleGetDefaults)
		r.Put("/defaults", s.handlePutDefaults)

		r.Get("/notifications", s.handleNotifications)
		r.Post("/notifications/read-all", s.handleNotificationsReadAll)
		r.Post("/notifications/{id}/read", s.handleNotificationRead)

		r.Route("/tasks", func(r chi.Router) {
			r.Post("/", s.handleCreateTask)
			r.Get("/", s.handleListTasks)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", s.handleGetTask)
				r.Patch("/", s.handleUpdateTask)
				r.Delete("/", s.handleDeleteTask)
				r.Post("/move", s.handleMoveTask)
				r.Patch("/color", s.handleColorTask)
				r.Get("/activity", s.handleTaskActivity)
			})
		})
	})

	staticFS, _ := fs.Sub(web.Static, "static")
	fileServer := http.FileServer(http.FS(staticFS))
	// Интерфейс встроен в exe: кешировать нельзя, иначе при обновлении
	// приложения WebView2 может показать старую версию страницы.
	noCache := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		fileServer.ServeHTTP(w, r)
	})
	r.Get("/*", func(w http.ResponseWriter, req *http.Request) {
		// SPA: неизвестные пути отдают index.html, известные — файлы.
		path := req.URL.Path
		if path != "/" && staticExists(staticFS, path) {
			noCache.ServeHTTP(w, req)
			return
		}
		req.URL.Path = "/"
		noCache.ServeHTTP(w, req)
	})

	return r
}

func staticExists(fsys fs.FS, path string) bool {
	stripped := path
	if len(stripped) > 0 && stripped[0] == '/' {
		stripped = stripped[1:]
	}
	if stripped == "" {
		return false
	}
	_, err := fs.Stat(fsys, stripped)
	return err == nil
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Actor")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("паника в обработчике", "panic", rec, "path", r.URL.Path)
				writeError(w, http.StatusInternalServerError, "внутренняя ошибка сервера")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		s.log.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", middleware.GetReqID(r.Context()),
		)
	})
}
