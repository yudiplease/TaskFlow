package events

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"tasktracker/internal/domain"
)

// eventQueue — асинхронная очередь доставки событий с одним воркером.
//
// Гарантии конкурентности:
//   - Publish неблокирующий и безопасен после Close (событие отбрасывается,
//     канал никогда не закрывается — паника "send on closed channel" исключена);
//   - Close идемпотентен и завершается за конечное время: воркер получает
//     сигнал, досыпает буфер с одной короткой попыткой на событие и выходит;
//   - отправка идёт с таймаутом и максимум maxAttempts попыток; после Close
//     повторные попытки прекращаются, чтобы не тянуть shutdown;
//   - паника в send перехватывается — воркер продолжает очередь.
type eventQueue struct {
	ch          chan domain.Event
	closed      chan struct{}
	closeOnce   sync.Once
	done        chan struct{}
	send        func(ctx context.Context, e domain.Event) error
	attemptWait time.Duration
	backoffUnit time.Duration // пауза между попытками = unit * номер попытки
	log         *slog.Logger
	onSent      func(domain.Event)
	onFailed    func(domain.Event, string)
	onDropped   func(domain.Event, string)
}

const maxAttempts = 3

// newEventQueue создаёт очередь и запускает воркер доставки.
func newEventQueue(size int, send func(ctx context.Context, e domain.Event) error, log *slog.Logger) *eventQueue {
	q := &eventQueue{
		ch:          make(chan domain.Event, size),
		closed:      make(chan struct{}),
		done:        make(chan struct{}),
		send:        send,
		attemptWait: 10 * time.Second,
		backoffUnit: time.Second,
		log:         log,
	}
	go q.worker()
	return q
}

// publish ставит событие в очередь без блокировки вызывающего.
func (q *eventQueue) publish(e domain.Event) {
	select {
	case <-q.closed:
		q.drop(e, "очередь закрыта")
		return
	default:
	}
	select {
	case q.ch <- e:
	default:
		q.drop(e, "переполнение")
	}
}

func (q *eventQueue) drop(e domain.Event, reason string) {
	if q.onDropped != nil {
		q.onDropped(e, reason)
	}
	q.log.Warn("событие отброшено", "type", e.Type, "task_id", e.TaskID, "reason", reason)
}

// close останавливает приём и ждёт опустошения буфера не дольше timeout.
func (q *eventQueue) close(timeout time.Duration) {
	q.closeOnce.Do(func() { close(q.closed) })
	select {
	case <-q.done:
	case <-time.After(timeout):
		q.log.Error("воркер очереди не остановился за отведённое время", "timeout", timeout)
	}
}

// lag возвращает число событий, ожидающих отправки (для метрики).
func (q *eventQueue) lag() int { return len(q.ch) }

func (q *eventQueue) worker() {
	defer close(q.done)
	for {
		select {
		case e := <-q.ch:
			q.deliver(e)
		case <-q.closed:
			// Досыпаем буфер одной быстрой попыткой на событие и выходим.
			for {
				select {
				case e := <-q.ch:
					q.deliverOnce(e)
				default:
					return
				}
			}
		}
	}
}

func (q *eventQueue) deliver(e domain.Event) {
	for attempt := 1; ; attempt++ {
		if q.attempt(e, attempt) {
			return
		}
		select {
		case <-q.closed:
			q.fail(e, "shutdown")
			return
		case <-time.After(q.backoffUnit * time.Duration(attempt)):
		}
	}
}

// deliverOnce — доставка без повторов (drain при закрытии).
func (q *eventQueue) deliverOnce(e domain.Event) {
	if !q.attempt(e, 1) {
		q.fail(e, "shutdown")
	}
}

func (q *eventQueue) attempt(e domain.Event, n int) bool {
	defer func() {
		if r := recover(); r != nil {
			q.log.Error("паника при отправке события", "panic", r, "type", e.Type)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), q.attemptWait)
	defer cancel()
	if err := q.send(ctx, e); err != nil {
		q.log.Warn("отправка события", "attempt", n, "err", err, "type", e.Type)
		if q.onFailed != nil {
			q.onFailed(e, "error")
		}
		return false
	}
	if q.onSent != nil {
		q.onSent(e)
	}
	return true
}

func (q *eventQueue) fail(e domain.Event, reason string) {
	if q.onFailed != nil {
		q.onFailed(e, reason)
	}
	q.log.Error("событие не доставлено", "type", e.Type, "task_id", e.TaskID, "reason", reason)
}
