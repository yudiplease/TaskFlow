package updatechecker

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

// discardLogger — тихий логгер для тестов.
func discardLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// memStore — in-memory Store для тестов CachedChecker.
type memStore struct {
	rel     *Release
	etag    string
	st      *UpdateState
	upserts int
}

func (m *memStore) GetReleaseCache(context.Context) (*Release, string, error) {
	if m.rel == nil {
		return nil, "", ErrNotFound
	}
	cp := *m.rel
	return &cp, m.etag, nil
}

func (m *memStore) UpsertReleaseCache(_ context.Context, r *Release, etag string) error {
	cp := *r
	m.rel, m.etag, m.upserts = &cp, etag, m.upserts+1
	m.touchState(r.CheckedAt)
	return nil
}

func (m *memStore) TouchReleaseCache(_ context.Context, checkedAt time.Time) error {
	if m.rel == nil {
		return ErrNotFound
	}
	m.rel.CheckedAt = checkedAt
	m.touchState(checkedAt)
	return nil
}

func (m *memStore) GetUpdateState(context.Context) (*UpdateState, error) {
	if m.st == nil {
		return nil, ErrNotFound
	}
	cp := *m.st
	return &cp, nil
}

func (m *memStore) UpsertUpdateState(_ context.Context, st *UpdateState) error {
	cp := *st
	m.st = &cp
	return nil
}

func (m *memStore) touchState(at time.Time) {
	if m.st == nil {
		m.st = DefaultState()
	}
	m.st.LastCheckedAt = &at
	m.st.NextCheckAfter = nil
}

// fakeChecker — подменяет GitHubChecker и считает вызовы «сети».
type fakeChecker struct {
	calls   int
	gotETag string
	rel     *Release
	etagOut string
	err     error
}

func (f *fakeChecker) SetETag(e string) { f.gotETag = e }

func (f *fakeChecker) Check(context.Context) (*Release, string, error) {
	f.calls++
	return f.rel, f.etagOut, f.err
}

func newCached(t *testing.T, ttl time.Duration, store *memStore, inner *fakeChecker, now func() time.Time) *CachedChecker {
	t.Helper()
	c := NewCachedChecker(inner, store, ttl, discardLogger(t))
	if now != nil {
		c.now = now
	}
	return c
}

func TestCachedCheckerFreshCacheSkipsNetwork(t *testing.T) {
	now := time.Now()
	store := &memStore{rel: &Release{Tag: "v1.4.0", CheckedAt: now}, st: DefaultState()}
	inner := &fakeChecker{rel: &Release{Tag: "v2.0.0"}}
	c := newCached(t, time.Hour, store, inner, func() time.Time { return now })

	rel, err := c.Check(context.Background())
	if err != nil {
		t.Fatalf("ошибка: %v", err)
	}
	if inner.calls != 0 {
		t.Errorf("свежий кэш: сеть не должна вызываться, вызовов %d", inner.calls)
	}
	if rel.Tag != "v1.4.0" {
		t.Errorf("из кэша: %q, хочу v1.4.0", rel.Tag)
	}
}

func TestCachedCheckerStaleCacheHitsNetwork(t *testing.T) {
	now := time.Now()
	store := &memStore{rel: &Release{Tag: "v1.3.2", CheckedAt: now.Add(-2 * time.Hour)}, st: DefaultState()}
	inner := &fakeChecker{rel: &Release{Tag: "v1.4.0"}, etagOut: `"e2"`}
	c := newCached(t, time.Hour, store, inner, func() time.Time { return now })

	rel, err := c.Check(context.Background())
	if err != nil {
		t.Fatalf("ошибка: %v", err)
	}
	if inner.calls != 1 {
		t.Fatalf("устаревший кэш: сеть должна вызваться 1 раз, было %d", inner.calls)
	}
	if rel.Tag != "v1.4.0" {
		t.Errorf("новый тег: %q, хочу v1.4.0", rel.Tag)
	}
	if store.rel.Tag != "v1.4.0" || store.etag != `"e2"` {
		t.Errorf("кэш не обновился: tag=%s etag=%s", store.rel.Tag, store.etag)
	}
	if store.st.LastCheckedAt == nil {
		t.Error("last_checked_at не обновлён")
	}
}

func TestCachedCheckerCheckNowBypassesTTL(t *testing.T) {
	now := time.Now()
	store := &memStore{rel: &Release{Tag: "v1.4.0", CheckedAt: now}, st: DefaultState()}
	inner := &fakeChecker{rel: &Release{Tag: "v1.4.0"}}
	c := newCached(t, time.Hour, store, inner, func() time.Time { return now })

	if _, err := c.CheckNow(context.Background()); err != nil {
		t.Fatalf("ошибка: %v", err)
	}
	if inner.calls != 1 {
		t.Errorf("CheckNow должен ходить в сеть независимо от TTL, вызовов %d", inner.calls)
	}
}

func TestCachedChecker304RefreshesCheckedAt(t *testing.T) {
	now := time.Now()
	checked := now.Add(-2 * time.Hour)
	store := &memStore{rel: &Release{Tag: "v1.3.2", CheckedAt: checked}, etag: `"e1"`, st: DefaultState()}
	inner := &fakeChecker{} // 304: (nil, "", nil)
	c := newCached(t, time.Hour, store, inner, func() time.Time { return now })

	rel, err := c.Check(context.Background())
	if err != nil {
		t.Fatalf("ошибка: %v", err)
	}
	if inner.calls != 1 {
		t.Errorf("сеть должна вызваться 1 раз, было %d", inner.calls)
	}
	if store.rel.Tag != "v1.3.2" {
		t.Errorf("latest_tag прежний: %q, хочу v1.3.2", store.rel.Tag)
	}
	if !store.rel.CheckedAt.Equal(now) {
		t.Errorf("checked_at обновлён: %v, хочу %v", store.rel.CheckedAt, now)
	}
	if rel.CheckedAt.IsZero() {
		t.Error("возвращаемый релиз без свежего checked_at")
	}
	if inner.gotETag != `"e1"` {
		t.Errorf("в сеть должен уйти прежний ETag, получено %q", inner.gotETag)
	}
}

func TestCachedCheckerRateLimitSetsNextCheckAfter(t *testing.T) {
	now := time.Now()
	store := &memStore{rel: &Release{Tag: "v1.3.2", CheckedAt: now.Add(-2 * time.Hour)}, st: DefaultState()}
	inner := &fakeChecker{err: &RateLimitError{StatusCode: 403, RetryAfter: 2 * time.Minute}}
	c := newCached(t, time.Hour, store, inner, func() time.Time { return now })

	if _, err := c.Check(context.Background()); err == nil {
		t.Fatal("rate-limit должен вернуть ошибку")
	}
	if store.st.NextCheckAfter == nil {
		t.Fatal("next_check_after не записан")
	}
	// max(RetryAfter=2m, 1h) = 1h
	if want := now.Add(time.Hour); !store.st.NextCheckAfter.Equal(want) {
		t.Errorf("next_check_after = %v, хочу %v", store.st.NextCheckAfter, want)
	}
}

// Обычная ошибка (сеть/5xx) тоже паузит: next_check_after = now + 1h.
func TestCachedCheckerGenericErrorSetsOneHourPause(t *testing.T) {
	now := time.Now()
	store := &memStore{rel: &Release{Tag: "v1.3.2", CheckedAt: now.Add(-2 * time.Hour)}, st: DefaultState()}
	inner := &fakeChecker{err: context.DeadlineExceeded}
	c := newCached(t, time.Hour, store, inner, func() time.Time { return now })

	if _, err := c.Check(context.Background()); err == nil {
		t.Fatal("ошибка сети должна вернуться наверх")
	}
	if want := now.Add(time.Hour); store.st.NextCheckAfter == nil || !store.st.NextCheckAfter.Equal(want) {
		t.Errorf("next_check_after = %v, хочу %v", store.st.NextCheckAfter, want)
	}
}

func TestCachedCheckerSetSettings(t *testing.T) {
	store := &memStore{st: DefaultState()}
	inner := &fakeChecker{}
	c := newCached(t, time.Hour, store, inner, nil)

	if err := c.SetSettings(context.Background(), false, 72); err != nil {
		t.Fatalf("ошибка: %v", err)
	}
	if store.st.Enabled || store.st.IntervalHours != 72 {
		t.Errorf("настройки не сохранились: %+v", store.st)
	}
	// Невалидный интервал откатывается к 24.
	if err := c.SetSettings(context.Background(), true, 55); err != nil {
		t.Fatalf("ошибка: %v", err)
	}
	if store.st.IntervalHours != 24 || !store.st.Enabled {
		t.Errorf("интервал 55 должен откатиться к 24: %+v", store.st)
	}
}

// Устаревший кэш не должен включать баннер, если проверка давно не удавалась
// (краевой случай «офлайн + старый кэш — тихо»).
func TestStatusStaleCacheIsSilent(t *testing.T) {
	now := time.Now()
	store := &memStore{
		rel: &Release{Tag: "v9.0.0", CheckedAt: now.Add(-48 * time.Hour)},
		st:  &UpdateState{Enabled: true, IntervalHours: 24},
	}
	inner := &fakeChecker{}
	c := newCached(t, time.Hour, store, inner, func() time.Time { return now })

	st, err := c.Status(context.Background(), "v1.3.2")
	if err != nil {
		t.Fatalf("ошибка: %v", err)
	}
	if st.UpdateAvailable {
		t.Error("кэш старше интервала — баннер показывать нельзя")
	}
}
