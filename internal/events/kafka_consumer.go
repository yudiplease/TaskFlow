package events

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/segmentio/kafka-go"

	"tasktracker/internal/domain"
	"tasktracker/internal/metrics"
	"tasktracker/internal/storage"
)

// insertTimeout — бюджет на запись одного события в PostgreSQL:
// зависшая вставка не блокирует консьюмер навсегда (событие вернётся
// повторной доставкой, потому что офсет не коммитится).
const insertTimeout = 10 * time.Second

// Consumer читает события задач из Kafka и записывает их в журнал
// активности (PostgreSQL). Подписка идёт группой, офсеты коммитятся
// только после успешной вставки — события не теряются при рестарте.
type Consumer struct {
	reader    *kafka.Reader
	activity  storage.ActivityRepository
	log       *slog.Logger
	metrics   *metrics.Metrics
	closeOnce sync.Once
}

// NewConsumer создаёт читателя топика событий.
func NewConsumer(brokers []string, topic, group string, repo storage.ActivityRepository, log *slog.Logger, m *metrics.Metrics) *Consumer {
	return &Consumer{
		reader: kafka.NewReader(kafka.ReaderConfig{
			Brokers:     brokers,
			Topic:       topic,
			GroupID:     group,
			MinBytes:    1,
			MaxBytes:    10 << 20,
			StartOffset: kafka.FirstOffset,
			MaxWait:     500 * time.Millisecond,
		}),
		activity: repo,
		log:      log,
		metrics:  m,
	}
}

// Run блокирует текущую горутину, читая события до отмены контекста.
// Ошибки брокера и паники обработки логируются — процесс не падает.
func (c *Consumer) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if !c.fetchAndHandle(ctx) {
			// Пауза перед повторным подключением; выход при отмене.
			select {
			case <-time.After(3 * time.Second):
			case <-ctx.Done():
				return
			}
		}
	}
}

// fetchAndHandle читает одно сообщение и обрабатывает его с защитой
// от паники. Возвращает false, когда чтение не удалось.
func (c *Consumer) fetchAndHandle(ctx context.Context) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			c.log.Error("паника при обработке сообщения Kafka", "panic", r)
			ok = false
		}
	}()

	msg, err := c.reader.FetchMessage(ctx)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			return true // штатное завершение, без паузы
		}
		c.log.Warn("чтение из Kafka", "err", err)
		return false
	}

	var e domain.Event
	if err := json.Unmarshal(msg.Value, &e); err != nil {
		// Битое сообщение не спасти — коммитируем и идём дальше.
		c.log.Error("разбор события, пропуск", "err", err, "partition", msg.Partition, "offset", msg.Offset)
	} else if !c.handle(ctx, e) {
		return false // вставка не удалась: без коммита, повторим позже
	}

	if err := c.reader.CommitMessages(ctx, msg); err != nil && ctx.Err() == nil {
		c.log.Warn("коммит офсета", "err", err)
	}
	return true
}

// handle записывает событие в журнал с ограниченным бюджетом времени.
func (c *Consumer) handle(_ context.Context, e domain.Event) bool {
	ctx, cancel := context.WithTimeout(context.Background(), insertTimeout)
	defer cancel()

	created := time.Now().UTC()
	if !e.Timestamp.IsZero() {
		created = e.Timestamp
	}
	act := domain.Activity{
		TaskID:    e.TaskID,
		Type:      e.Type,
		Actor:     e.Actor,
		Payload:   e.Payload,
		CreatedAt: created,
	}
	if err := c.activity.Insert(ctx, &act); err != nil {
		c.log.Error("запись активности, событие будет перечитано", "err", err)
		return false
	}
	c.metrics.EventsConsumedTotal.WithLabelValues(e.Type).Inc()
	return true
}

// Close освобождает ресурсы читателя; безопасно вызывать многократно.
func (c *Consumer) Close() error {
	var err error
	c.closeOnce.Do(func() { err = c.reader.Close() })
	return err
}
