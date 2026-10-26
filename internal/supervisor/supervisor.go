// Package supervisor управляет фоновыми горутинами приложения:
// перехватывает паники, перезапускает упавшие воркеры с экспоненциальной
// паузой и останавливает всё по отмене контекста за ограниченное время.
//
// Гарантии:
//   - паника воркера не роняет процесс: логируется, воркер перезапускается;
//   - воркер в состоянии crash-loop (падает чаще maxRestarts подряд)
//     выводится из работы с ошибкой в логе, остальные продолжают;
//   - Wait всегда завершается за конечное время (deadline).
package supervisor

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const (
	defaultMaxRestarts = 5
	defaultBaseBackoff = 200 * time.Millisecond
	defaultMaxBackoff  = 5 * time.Second
	defaultGracePeriod = 3 * time.Second // меньше — падение считается crash-циклом
)

// Options — параметры контроля воркеров (все поля необязательны).
type Options struct {
	MaxRestarts int
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
	GracePeriod time.Duration
}

func (o Options) withDefaults() Options {
	if o.MaxRestarts <= 0 {
		o.MaxRestarts = defaultMaxRestarts
	}
	if o.BaseBackoff <= 0 {
		o.BaseBackoff = defaultBaseBackoff
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = defaultMaxBackoff
	}
	if o.GracePeriod <= 0 {
		o.GracePeriod = defaultGracePeriod
	}
	return o
}

// Group — набор контролируемых воркеров.
type Group struct {
	log    *slog.Logger
	wg     sync.WaitGroup
	cancel context.CancelFunc
	ctx    context.Context
	opts   Options
}

// NewGroup создаёт группу с собственным производным контекстом.
func NewGroup(parent context.Context, log *slog.Logger, opts Options) *Group {
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(parent)
	return &Group{log: log, ctx: ctx, cancel: cancel, opts: opts.withDefaults()}
}

// Go запускает именованный воркер. Функция должна возвращать по отмене
// контекста; она получает ctx группы и может быть перезапущена.
func (g *Group) Go(name string, fn func(ctx context.Context)) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		g.supervise(name, fn)
	}()
}

func (g *Group) supervise(name string, fn func(ctx context.Context)) {
	restarts := 0
	for {
		started := time.Now()
		err := runRecovered(g.ctx, name, fn, g.log)

		if g.ctx.Err() != nil {
			return // штатная остановка
		}

		if err == nil && time.Since(started) >= g.opts.GracePeriod {
			restarts = 0 // воркер отработал достаточно долго — счётчик сбрасывается
		}
		restarts++

		if restarts > g.opts.MaxRestarts {
			g.log.Error("воркер остановлен: превышен лимит перезапусков",
				"worker", name, "restarts", restarts-1)
			return
		}

		backoff := g.opts.BaseBackoff * (1 << (restarts - 1)) // экспоненциально
		if backoff > g.opts.MaxBackoff {
			backoff = g.opts.MaxBackoff
		}
		g.log.Warn("воркер перезапускается",
			"worker", name, "restart", restarts, "backoff", backoff,
			"reason", errReason(err))

		select {
		case <-time.After(backoff):
		case <-g.ctx.Done():
			return
		}
	}
}

// runRecovered выполняет fn, превращая панику в ошибку.
func runRecovered(ctx context.Context, name string, fn func(ctx context.Context), log *slog.Logger) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &PanicError{Worker: name, Value: r}
			log.Error("паника в воркере", "worker", name, "panic", r)
		}
	}()
	fn(ctx)
	return nil
}

// PanicError — паника, перехваченная в воркере.
type PanicError struct {
	Worker string
	Value  any
}

func (e *PanicError) Error() string { return "паника в воркере " + e.Worker }

func errReason(err error) string {
	if err == nil {
		return "быстрое завершение"
	}
	return err.Error()
}

// Wait останавливает группу (cancel) и ждёт воркеров не дольше timeout.
// Возвращает true, если все завершились вовремя.
func (g *Group) Wait(timeout time.Duration) bool {
	g.cancel()

	done := make(chan struct{})
	go func() {
		g.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-time.After(timeout):
		g.log.Error("не все воркеры остановились за отведённое время", "timeout", timeout)
		return false
	}
}

// Stop отменяет контекст группы, не дожидаясь воркеров.
func (g *Group) Stop() { g.cancel() }
