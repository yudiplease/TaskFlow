package supervisor_test

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"tasktracker/internal/supervisor"
)

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fast — группа с миллисекундными бэкоффами, чтобы тесты не ждали секунды.
var fast = supervisor.Options{BaseBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond}

func TestWorkerRunsUntilCancel(t *testing.T) {
	g := supervisor.NewGroup(context.Background(), testLog(), fast)
	var calls atomic.Int32
	g.Go("w", func(ctx context.Context) {
		calls.Add(1)
		<-ctx.Done() // блокируется до отмены — нормальный режим
	})
	time.Sleep(100 * time.Millisecond)
	if !g.Wait(2 * time.Second) {
		t.Fatal("Wait не дождался воркера")
	}
	if calls.Load() != 1 {
		t.Errorf("вызовов = %d, want 1 (перезапусков быть не должно)", calls.Load())
	}
}

func TestPanicRestartsWorker(t *testing.T) {
	g := supervisor.NewGroup(context.Background(), testLog(), fast)
	var calls atomic.Int32
	g.Go("panicky", func(ctx context.Context) {
		n := calls.Add(1)
		if n <= 2 {
			panic("бах")
		}
		<-ctx.Done()
	})
	time.Sleep(50 * time.Millisecond)
	if !g.Wait(3 * time.Second) {
		t.Fatal("Wait не дождался воркера")
	}
	if calls.Load() != 3 {
		t.Errorf("вызовов = %d, want 3 (2 паники + 1 успешный запуск)", calls.Load())
	}
}

func TestCrashLoopStopsAfterMaxRestarts(t *testing.T) {
	g := supervisor.NewGroup(context.Background(), testLog(), fast)
	var calls atomic.Int32
	g.Go("looper", func(ctx context.Context) {
		calls.Add(1)
		panic("всегда падаю")
	})
	// 1 исходный запуск + 5 перезапусков на миллисекундных бэкоффах
	time.Sleep(200 * time.Millisecond)
	if !g.Wait(5 * time.Second) {
		t.Fatal("crash-loop воркер не остановился")
	}
	if got := calls.Load(); got != 6 {
		t.Errorf("вызовов = %d, want 6", got)
	}
}

func TestFastReturnCountsAsRestart(t *testing.T) {
	g := supervisor.NewGroup(context.Background(), testLog(), fast)
	var calls atomic.Int32
	g.Go("quick", func(ctx context.Context) {
		calls.Add(1)
		// мгновенно возвращается без отмены контекста
	})
	time.Sleep(200 * time.Millisecond)
	if !g.Wait(5 * time.Second) {
		t.Fatal("воркер не остановился")
	}
	if got := calls.Load(); got != 6 {
		t.Errorf("вызовов = %d, want 6 (лимит перезапусков для мгновенных выходов)", got)
	}
}

func TestConcurrentWorkers(t *testing.T) {
	g := supervisor.NewGroup(context.Background(), testLog(), fast)
	var total atomic.Int32
	for i := 0; i < 20; i++ {
		g.Go("w", func(ctx context.Context) {
			total.Add(1)
			<-ctx.Done()
		})
	}
	time.Sleep(100 * time.Millisecond)
	if !g.Wait(2 * time.Second) {
		t.Fatal("Wait не дождался воркеров")
	}
	if total.Load() != 20 {
		t.Errorf("отработало воркеров = %d, want 20", total.Load())
	}
}

func TestPublishAfterGroupCancel(t *testing.T) {
	// Группа с уже отменённым родительским контекстом: воркеры должны сразу выйти.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	g := supervisor.NewGroup(ctx, testLog(), fast)
	g.Go("w", func(ctx context.Context) { <-ctx.Done() })
	if !g.Wait(time.Second) {
		t.Fatal("Wait завис при отменённом контексте")
	}
}
