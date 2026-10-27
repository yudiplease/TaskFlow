package updatechecker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// MinRateLimitPause — минимальная пауза после 403/429 (спецификация MVP:
// next_check_after = now + max(RetryAfter, 1h) — не спамить API).
const MinRateLimitPause = time.Hour

// DefaultInterval — интервал проверки, пока пользователь не выбрал свой.
const DefaultInterval = 24 * time.Hour

// CachedChecker — обёртка над Checker с кэшем в Store:
// свежий кэш (моложе ttl) закрывает запрос без сети.
type CachedChecker struct {
	inner Checker
	store Store
	ttl   time.Duration
	log   *slog.Logger
	now   func() time.Time

	mu sync.Mutex // воркер и кнопка «Проверить сейчас» не должны чекать параллельно
}

// NewCachedChecker собирает обёртку. ttl <= 0 → 24 часа.
func NewCachedChecker(inner Checker, store Store, ttl time.Duration, log *slog.Logger) *CachedChecker {
	if ttl <= 0 {
		ttl = DefaultInterval
	}
	if log == nil {
		log = slog.Default()
	}
	return &CachedChecker{inner: inner, store: store, ttl: ttl, log: log, now: time.Now}
}

// Check проверяет обновления с учётом TTL: свежий кэш — сеть не вызывается.
func (c *CachedChecker) Check(ctx context.Context) (*Release, error) {
	return c.refresh(ctx, true)
}

// CheckNow проверяет принудительно, минуя TTL (кнопка «Проверить сейчас»).
// ETag всё равно используется: без изменений GitHub ответит 304.
func (c *CachedChecker) CheckNow(ctx context.Context) (*Release, error) {
	return c.refresh(ctx, false)
}

func (c *CachedChecker) refresh(ctx context.Context, respectTTL bool) (*Release, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	cached, etag, err := c.store.GetReleaseCache(ctx)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if cached == nil {
		etag = ""
	}
	if respectTTL && cached != nil && c.now().Sub(cached.CheckedAt) < c.ttl {
		return cached, nil // свежий кэш — сеть не трогаем
	}
	c.inner.SetETag(etag)

	rel, newEtag, err := c.inner.Check(ctx)
	switch {
	case err != nil:
		// На любую ошибку — пауза: rate-limit согласно Retry-After,
		// прочие (сеть, 5xx, черновик) — минимум час. Не спамим API.
		pause := MinRateLimitPause
		var rl *RateLimitError
		if errors.As(err, &rl) {
			pause = max(rl.RetryAfter, MinRateLimitPause)
		}
		c.pauseAfterError(ctx, pause)
		return nil, err

	case rel != nil: // HTTP 200
		rel.CheckedAt = c.now()
		if err := c.store.UpsertReleaseCache(ctx, rel, newEtag); err != nil {
			// Релиз получен, но не сохранился: отвечаем как есть, после
			// перезапуска чек повторится.
			c.log.Warn("сохранение кэша релиза", "err", err)
		}
		return rel, nil

	default: // HTTP 304 — данные не менялись, продлеваем время проверки
		if cached == nil {
			return nil, nil // 304 без кэша бессмыслен — трактуем как «нет данных»
		}
		checkedAt := c.now()
		if err := c.store.TouchReleaseCache(ctx, checkedAt); err != nil {
			c.log.Warn("продление времени проверки (304)", "err", err)
		}
		cached.CheckedAt = checkedAt
		return cached, nil
	}
}

// pauseAfterError записывает next_check_after = now + pause.
func (c *CachedChecker) pauseAfterError(ctx context.Context, pause time.Duration) {
	st, err := c.stateOrDefault(ctx)
	if err != nil {
		c.log.Warn("чтение состояния обновлений", "err", err)
		return
	}
	after := c.now().Add(pause)
	st.NextCheckAfter = &after
	if err := c.store.UpsertUpdateState(ctx, st); err != nil {
		c.log.Warn("запись next_check_after", "err", err)
	}
}

// Status — снимок состояния для UI (баннер и секция настроек).
//
// UpdateAvailable считается только по кэшу, проверенному не позже одного
// цикла проверки назад: если приложение долго было офлайн и кэш устарел,
// а сеть недоступна — баннер молчит (данные старые).
func (c *CachedChecker) Status(ctx context.Context, appVersion string) (*Status, error) {
	st, err := c.stateOrDefault(ctx)
	if err != nil {
		return nil, err
	}
	rel, _, err := c.store.GetReleaseCache(ctx)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	s := &Status{
		AppVersion:     appVersion,
		Enabled:        st.Enabled,
		IntervalHours:  st.IntervalHours,
		LastCheckedAt:  st.LastCheckedAt,
		NextCheckAfter: st.NextCheckAfter,
		Latest:         rel,
	}
	if rel != nil {
		fresh := c.now().Sub(rel.CheckedAt) < intervalOf(st)
		s.UpdateAvailable = fresh && ShouldNotify(rel.Tag, appVersion, st)
	}
	return s, nil
}

// SetSettings сохраняет чекбокс и интервал из настроек (применяется сразу).
func (c *CachedChecker) SetSettings(ctx context.Context, enabled bool, intervalHours int) error {
	st, err := c.stateOrDefault(ctx)
	if err != nil {
		return err
	}
	st.Enabled = enabled
	if !validInterval(intervalHours) {
		intervalHours = int(DefaultInterval / time.Hour)
	}
	st.IntervalHours = intervalHours
	return c.store.UpsertUpdateState(ctx, st)
}

func (c *CachedChecker) stateOrDefault(ctx context.Context) (*UpdateState, error) {
	st, err := c.store.GetUpdateState(ctx)
	if errors.Is(err, ErrNotFound) {
		return DefaultState(), nil
	}
	return st, err
}

// Status — payload GET /api/updates/state и POST /api/updates/check.
type Status struct {
	AppVersion      string     `json:"app_version"`
	Enabled         bool       `json:"enabled"`
	IntervalHours   int        `json:"interval_hours"`
	LastCheckedAt   *time.Time `json:"last_checked_at"`
	NextCheckAfter  *time.Time `json:"next_check_after,omitempty"`
	Latest          *Release   `json:"latest,omitempty"`
	UpdateAvailable bool       `json:"update_available"`
}

// Allowed intervals для селекта настроек (MVP: 24 / 72 / 168 часов).
func validInterval(hours int) bool {
	return hours == 24 || hours == 72 || hours == 168
}

func intervalOf(st *UpdateState) time.Duration {
	if st.IntervalHours > 0 {
		return time.Duration(st.IntervalHours) * time.Hour
	}
	return DefaultInterval
}
