package events

import (
	"context"
	"log/slog"

	"tasktracker/internal/domain"
)

// LocalBus — событийная шина для десктопной версии: доставляет события
// обработчику синхронно в порядке публикации (FIFO без сети и Kafka).
// Паника обработчика перехватывается, чтобы не уронить вызывающего.
type LocalBus struct {
	handler func(ctx context.Context, e domain.Event)
	log     *slog.Logger
}

// NewLocalBus создаёт шину с обработчиком событий.
func NewLocalBus(log *slog.Logger, handler func(ctx context.Context, e domain.Event)) *LocalBus {
	return &LocalBus{handler: handler, log: log}
}

// Publish доставляет событие обработчику немедленно.
func (b *LocalBus) Publish(ctx context.Context, e domain.Event) {
	defer func() {
		if r := recover(); r != nil {
			b.log.Error("паника в обработчике локальной шины событий", "panic", r, "type", e.Type)
		}
	}()
	b.handler(ctx, e)
}

// Close — no-op (сетевых ресурсов нет).
func (b *LocalBus) Close() {}

var _ Publisher = (*LocalBus)(nil)
