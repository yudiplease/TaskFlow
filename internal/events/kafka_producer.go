package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/segmentio/kafka-go"

	"tasktracker/internal/domain"
	"tasktracker/internal/metrics"
)

// KafkaPublisher публикует события в Kafka асинхронно: Publish кладёт событие
// в буфер и сразу возвращается, горутина-воркер доставляет его брокеру.
// Ключ партиции = ID задачи, поэтому события одной задачи упорядочены.
type KafkaPublisher struct {
	writer *kafka.Writer
	queue  *eventQueue
	m      *metrics.Metrics
}

// NewKafkaPublisher создаёт продюсера и запускает фоновую доставку.
func NewKafkaPublisher(brokers []string, topic string, queueSize int, log *slog.Logger, m *metrics.Metrics) *KafkaPublisher {
	w := &kafka.Writer{
		Addr:         kafka.TCP(brokers...),
		Topic:        topic,
		Balancer:     &kafka.Hash{},
		RequiredAcks: kafka.RequireOne,
		BatchTimeout: 10 * time.Millisecond,
		Logger:       kafka.LoggerFunc(func(msg string, args ...any) { log.Debug("kafka writer: " + fmt.Sprintf(msg, args...)) }),
	}

	p := &KafkaPublisher{writer: w, m: m}
	p.queue = newEventQueue(queueSize, p.writeOne, log)
	p.queue.onSent = func(e domain.Event) {
		m.EventsProducedTotal.WithLabelValues(e.Type, "ok").Inc()
	}
	p.queue.onFailed = func(e domain.Event, reason string) {
		m.EventsProducedTotal.WithLabelValues(e.Type, reason).Inc()
	}
	p.queue.onDropped = func(e domain.Event, reason string) {
		m.EventsDroppedTotal.Inc()
	}
	return p
}

func (p *KafkaPublisher) writeOne(ctx context.Context, e domain.Event) error {
	value, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("кодирование события: %w", err)
	}
	return p.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(e.TaskID),
		Value: value,
		Time:  e.Timestamp,
	})
}

// Publish ставит событие в очередь. Неблокирующая операция: при переполнении
// буфера или после закрытия событие отбрасывается и инкрементируется метрика.
func (p *KafkaPublisher) Publish(ctx context.Context, e domain.Event) {
	p.queue.publish(e)
	p.m.EventsLag.Set(float64(p.queue.lag()))
}

// Close останавливает приём событий и закрывает writer за конечное время.
func (p *KafkaPublisher) Close() {
	p.queue.close(5 * time.Second)
	_ = p.writer.Close()
}

// EnsureTopic создаёт топик, если он ещё не существует (3 партиции, RF=1).
func EnsureTopic(ctx context.Context, brokers []string, topic string, log *slog.Logger) {
	if len(brokers) == 0 {
		return
	}
	conn, err := kafka.DialLeader(ctx, "tcp", brokers[0], topic, 0)
	if err == nil {
		_ = conn.Close()
		return // топик уже существует
	}
	conn, err = kafka.DialContext(ctx, "tcp", brokers[0])
	if err != nil {
		log.Warn("не удалось подключиться к Kafka для создания топика", "err", err)
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	err = conn.CreateTopics(kafka.TopicConfig{
		Topic:             topic,
		NumPartitions:     3,
		ReplicationFactor: 1,
	})
	if err != nil {
		log.Warn("создание топика", "topic", topic, "err", err)
	} else {
		log.Info("топик создан", "topic", topic)
	}
}

// BrokerPing проверяет доступность брокера для readiness-пробы.
func BrokerPing(brokers []string, timeout time.Duration) error {
	if len(brokers) == 0 {
		return fmt.Errorf("брокеры не заданы")
	}
	d := &kafka.Dialer{Timeout: timeout}
	conn, err := d.DialContext(context.Background(), "tcp", brokers[0])
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	_, err = conn.Brokers()
	return err
}
