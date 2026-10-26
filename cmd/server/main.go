// Сервер трекера задач: REST API, веб-интерфейс, Kafka-конвейер событий.
//
// Асинхронная модель:
//   - HTTP-обработчики синхронны к PostgreSQL (корректность ответа),
//     но публикация событий в Kafka неблокирующая (буфер + воркер);
//   - фоновые горутины (консьюмер Kafka, планировщик напоминаний)
//     запускаются через supervisor: паники перехватываются, упавшие
//     воркеры перезапускаются с backoff, shutdown ограничен по времени.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"tasktracker/internal/config"
	"tasktracker/internal/events"
	"tasktracker/internal/httpapi"
	"tasktracker/internal/metrics"
	"tasktracker/internal/notifier"
	"tasktracker/internal/service"
	"tasktracker/internal/storage/postgres"
	"tasktracker/internal/supervisor"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// PostgreSQL — обязателен: без него сервис не стартует.
	pool, err := postgres.New(ctx, cfg, log)
	if err != nil {
		log.Error("подключение к postgres", "err", err)
		os.Exit(1)
	}
	log.Info("postgres подключен")

	m := metrics.New()

	// Kafka — желательно: при недоступности сервис деградирует, но работает.
	var publisher events.Publisher
	kafkaHealthy := &atomic.Bool{}
	var consumer *events.Consumer

	workers := supervisor.NewGroup(ctx, log, supervisor.Options{})

	if cfg.KafkaEnabled {
		events.EnsureTopic(ctx, cfg.KafkaBrokers, cfg.KafkaTopic, log)
		publisher = events.NewKafkaPublisher(cfg.KafkaBrokers, cfg.KafkaTopic, 1024, log, m)
		kafkaHealthy.Store(true)

		consumer = events.NewConsumer(cfg.KafkaBrokers, cfg.KafkaTopic, cfg.KafkaGroup, postgres.NewActivity(pool), log, m)
		workers.Go("kafka-consumer", func(wctx context.Context) {
			log.Info("консьюмер событий запущен", "topic", cfg.KafkaTopic, "group", cfg.KafkaGroup)
			consumer.Run(wctx)
		})
	} else {
		publisher = events.NoopPublisher{}
		log.Warn("kafka отключён конфигурацией (KAFKA_ENABLED=false)")
	}

	svc := service.NewTaskService(postgres.NewTasks(pool), postgres.NewActivity(pool), publisher, m)
	notificationsSvc := service.NewNotifications(postgres.NewNotifications(pool))

	// Планировщик напоминаний: проверяет сроки раз в минуту.
	notif := notifier.New(postgres.NewTasks(pool), postgres.NewNotifications(pool), publisher, m, log)
	workers.Go("notifier", func(wctx context.Context) {
		notif.Run(wctx, time.Minute)
	})

	api := httpapi.NewServer(httpapi.Deps{
		Config:        cfg,
		Service:       svc,
		Notifications: notificationsSvc,
		Metrics:       m,
		Log:           log,
		PingPostgres: func() error {
			c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			return pool.Ping(c)
		},
		KafkaHealthy: func() bool { return !cfg.KafkaEnabled || kafkaHealthy.Load() },
	})

	srv := &http.Server{
		Addr:         ":" + strconv.Itoa(cfg.Port),
		Handler:      api.Handler(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	httpErr := make(chan error, 1)
	go func() {
		log.Info("http сервер запущен", "port", cfg.Port)
		httpErr <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		log.Info("получен сигнал завершения")
	case err := <-httpErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http сервер упал", "err", err)
		}
	}

	log.Info("завершение работы...")

	// Порядок остановки с ограниченным бюджетом на каждый шаг:
	// 1) перестаём принимать запросы и ждём активные обработчики;
	// 2) дожидаемся фоновых воркеров (supervisor.Wait с deadline);
	// 3) продюсер досыпает буфер в Kafka и закрывается;
	// 4) закрываем пул PostgreSQL.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown http", "err", err)
	}
	log.Info("http сервер остановлен")

	workers.Wait(cfg.ShutdownTimeout)
	if consumer != nil {
		_ = consumer.Close()
	}
	publisher.Close()

	pool.Close()
	log.Info("все компоненты остановлены")
}
