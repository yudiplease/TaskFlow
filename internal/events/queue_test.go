package events

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tasktracker/internal/domain"
)

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func ev(id int) domain.Event {
	return domain.Event{Type: "task.created", TaskID: string(rune('a' + id%26)), Timestamp: time.Now()}
}

func TestQueueDeliversFIFO(t *testing.T) {
	var mu sync.Mutex
	var got []string
	q := newEventQueue(16, func(_ context.Context, e domain.Event) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, e.TaskID)
		return nil
	}, testLog())

	for i := 0; i < 5; i++ {
		e := ev(i)
		e.TaskID = "t" + string(rune('0'+i))
		q.publish(e)
	}
	q.close(2 * time.Second)

	want := []string{"t0", "t1", "t2", "t3", "t4"}
	if len(got) != len(want) {
		t.Fatalf("доставлено %d из %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("порядок нарушен: got[%d]=%s, want %s (один воркер => FIFO)", i, got[i], want[i])
		}
	}
}

func TestQueueRetriesThenSucceeds(t *testing.T) {
	var attempts atomic.Int32
	q := newEventQueue(4, func(_ context.Context, e domain.Event) error {
		if attempts.Add(1) < 3 {
			return errors.New("брокер недоступен")
		}
		return nil
	}, testLog())
	q.attemptWait = 10 * time.Millisecond
	q.backoffUnit = 5 * time.Millisecond

	var sent atomic.Int32
	q.onSent = func(domain.Event) { sent.Add(1) }

	q.publish(ev(0))

	// Ждём, пока ретраи доведут событие до успеха (close после закрытия
	// повторы прекращает — это отдельный тест).
	deadline := time.After(5 * time.Second)
	for sent.Load() == 0 {
		select {
		case <-deadline:
			t.Fatalf("событие не доставлено после ретраев, attempts=%d", attempts.Load())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	q.close(2 * time.Second)

	if attempts.Load() != 3 {
		t.Errorf("попыток = %d, want 3", attempts.Load())
	}
}

func TestQueueGivesUpAfterMaxAttempts(t *testing.T) {
	var attempts atomic.Int32
	var failed atomic.Int32
	q := newEventQueue(4, func(_ context.Context, e domain.Event) error {
		attempts.Add(1)
		return errors.New("навсегда недоступен")
	}, testLog())
	q.attemptWait = 10 * time.Millisecond
	q.backoffUnit = 5 * time.Millisecond
	q.onFailed = func(domain.Event, string) { failed.Add(1) }

	q.publish(ev(0))
	// Дожидаемся, пока воркер исчерпает попытки (без close — close бы прервал).
	deadline := time.After(5 * time.Second)
	for failed.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("воркер не завершил попытки")
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
	if attempts.Load() != maxAttempts {
		t.Errorf("попыток = %d, want %d", attempts.Load(), maxAttempts)
	}
	q.close(2 * time.Second)
}

func TestQueuePublishAfterCloseDoesNotPanic(t *testing.T) {
	q := newEventQueue(4, func(_ context.Context, e domain.Event) error { return nil }, testLog())
	q.close(2 * time.Second)

	var dropped atomic.Int32
	q.onDropped = func(domain.Event, string) { dropped.Add(1) }

	// Главная проверка: паники "send on closed channel" быть не должно.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			q.publish(ev(i))
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publish после close завис")
	}
}

func TestQueueConcurrentPublishAndClose(t *testing.T) {
	var sent, dropped atomic.Int32
	q := newEventQueue(128, func(_ context.Context, e domain.Event) error {
		time.Sleep(time.Millisecond) // имитируем медленный брокер
		return nil
	}, testLog())
	q.onSent = func(domain.Event) { sent.Add(1) }
	q.onDropped = func(domain.Event, string) { dropped.Add(1) }

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				q.publish(ev(i))
			}
		}()
	}
	time.Sleep(30 * time.Millisecond)
	q.close(5 * time.Second) // параллельно с публикациями
	wg.Wait()

	// Ни одна горутина не зависла и не запаниковала; итог консистентен:
	// доставлено + отброшено покрывает часть публикаций после закрытия.
	if sent.Load() == 0 {
		t.Error("ни одно событие не доставлено")
	}
}

func TestQueueDrainsBufferOnClose(t *testing.T) {
	var sent atomic.Int32
	release := make(chan struct{})
	q := newEventQueue(64, func(_ context.Context, e domain.Event) error {
		<-release // блокируем первую отправку
		return nil
	}, testLog())
	q.onSent = func(domain.Event) { sent.Add(1) }

	for i := 0; i < 3; i++ {
		q.publish(ev(i))
	}
	time.Sleep(50 * time.Millisecond)
	close(release) // разблокируем доставку
	q.close(2 * time.Second)

	// Все события из буфера должны быть доставлены при закрытии.
	if sent.Load() != 3 {
		t.Errorf("доставлено при drain = %d, want 3", sent.Load())
	}
}

func TestQueueSendPanicDoesNotKillWorker(t *testing.T) {
	var calls atomic.Int32
	q := newEventQueue(8, func(_ context.Context, e domain.Event) error {
		n := calls.Add(1)
		if n == 1 {
			panic("внезапная паника в драйвере")
		}
		return nil
	}, testLog())
	var sent atomic.Int32
	q.onSent = func(domain.Event) { sent.Add(1) }

	q.publish(ev(0))
	q.publish(ev(1))
	q.close(3 * time.Second)

	// Второе событие должно быть доставлено несмотря на панику на первом.
	if sent.Load() == 0 {
		t.Error("воркер погиб после паники send")
	}
}
