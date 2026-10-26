// Package metrics регистрирует метрики Prometheus приложения.
package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics — коллекция счётчиков и гистограмм трекера.
type Metrics struct {
	HTTPRequestsTotal    *prometheus.CounterVec
	HTTPDurationSeconds  *prometheus.HistogramVec
	TaskOperationsTotal  *prometheus.CounterVec
	TasksByStatus        *prometheus.GaugeVec
	EventsProducedTotal  *prometheus.CounterVec
	EventsConsumedTotal  *prometheus.CounterVec
	EventsDroppedTotal   prometheus.Counter
	EventsLag            prometheus.Gauge
	NotificationsCreated *prometheus.CounterVec
}

// New создаёт и регистрирует метрики. Повторные вызовы возвращают
// один и тот же экземпляр: Prometheus не допускает дублей коллекторов.
func New() *Metrics {
	registerOnce.Do(func() {
		defaultMetrics = newMetrics()
		// Нулевые дети, чтобы векторы были видны в /metrics до первых событий.
		defaultMetrics.NotificationsCreated.WithLabelValues("due_soon")
		defaultMetrics.NotificationsCreated.WithLabelValues("overdue")
	})
	return defaultMetrics
}

var (
	registerOnce   sync.Once
	defaultMetrics *Metrics
)

func newMetrics() *Metrics {
	return &Metrics{
		HTTPRequestsTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Общее число HTTP-запросов по методу, маршруту и коду ответа.",
		}, []string{"method", "route", "code"}),
		HTTPDurationSeconds: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Длительность HTTP-запросов в секундах.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		}, []string{"method", "route"}),
		TaskOperationsTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "task_operations_total",
			Help: "Операции над задачами: create, update, delete, move.",
		}, []string{"op", "status"}),
		TasksByStatus: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "tasks_by_status",
			Help: "Текущее число задач в каждой колонке.",
		}, []string{"status"}),
		EventsProducedTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "events_produced_total",
			Help: "События, отправленные в Kafka, по типу.",
		}, []string{"type", "result"}),
		EventsConsumedTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "events_consumed_total",
			Help: "События, прочитанные из Kafka, по типу.",
		}, []string{"type"}),
		EventsDroppedTotal: promauto.NewCounter(prometheus.CounterOpts{
			Name: "events_dropped_total",
			Help: "События, отброшенные из-за переполнения очереди продюсера.",
		}),
		EventsLag: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "events_queue_lag",
			Help: "Число событий, ожидающих отправки в Kafka.",
		}),
		NotificationsCreated: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "notifications_created_total",
			Help: "Напоминания, созданные планировщиком, по типу.",
		}, []string{"type"}),
	}
}
