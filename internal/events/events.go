// Package events — публикация и потребление событий задач через Kafka.
package events

import (
	"context"

	"tasktracker/internal/domain"
)

// Publisher отправляет события задач. Реализации не должны блокировать вызывающего.
type Publisher interface {
	Publish(ctx context.Context, e domain.Event)
	Close()
}

// NoopPublisher используется, когда Kafka отключена конфигурацией.
type NoopPublisher struct{}

// Publish игнорирует событие.
func (NoopPublisher) Publish(context.Context, domain.Event) {}

// Close — no-op.
func (NoopPublisher) Close() {}
