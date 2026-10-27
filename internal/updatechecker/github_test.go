package updatechecker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// githubSrv — тестовый «GitHub»: отдаёт тело и код, запоминает заголовки запроса.
type githubSrv struct {
	ts     *httptest.Server
	status int
	body   string
	etag   string
	retry  string
	reqs   int
	ifNone string
}

func newGithubSrv(t *testing.T, status int, body, etag, retry string) *githubSrv {
	t.Helper()
	g := &githubSrv{status: status, body: body, etag: etag, retry: retry}
	g.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.reqs++
		g.ifNone = r.Header.Get("If-None-Match")
		if g.etag != "" {
			w.Header().Set("ETag", g.etag)
		}
		if g.retry != "" {
			w.Header().Set("Retry-After", g.retry)
		}
		w.WriteHeader(g.status)
		_, _ = w.Write([]byte(g.body))
	}))
	t.Cleanup(g.ts.Close)
	return g
}

func (g *githubSrv) checker() *GitHubChecker {
	c := NewGitHubChecker("yudiplease/TaskFlow")
	c.base = g.ts.URL
	return c
}

const releaseJSON = `{
  "tag_name": "v1.4.0",
  "name": "TaskFlow v1.4.0",
  "draft": false,
  "prerelease": false,
  "html_url": "https://github.com/yudiplease/TaskFlow/releases/tag/v1.4.0",
  "published_at": "2026-10-20T12:00:00Z"
}`

func TestGitHubChecker200(t *testing.T) {
	g := newGithubSrv(t, http.StatusOK, releaseJSON, `"v1.4.0"`, "")
	rel, etag, err := g.checker().Check(context.Background())
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if etag != `"v1.4.0"` {
		t.Errorf("etag = %q, хочу %q", etag, `"v1.4.0"`)
	}
	if rel == nil || rel.Tag != "v1.4.0" || rel.URL == "" {
		t.Fatalf("релиз распарсен неверно: %+v", rel)
	}
	if rel.PublishedAt.IsZero() {
		t.Error("published_at потерялся")
	}
}

func TestGitHubChecker304(t *testing.T) {
	g := newGithubSrv(t, http.StatusOK, releaseJSON, `"etag-1"`, "")
	c := g.checker()
	if _, _, err := c.Check(context.Background()); err != nil {
		t.Fatalf("первый чек: %v", err)
	}
	g.status, g.body, g.etag = http.StatusNotModified, "", `"etag-1"`

	rel, etag, err := c.Check(context.Background())
	if err != nil {
		t.Fatalf("304 не должен быть ошибкой: %v", err)
	}
	if rel != nil {
		t.Errorf("при 304 тело приходит nil, получено %+v", rel)
	}
	if etag != `"etag-1"` {
		t.Errorf("etag при 304 = %q, хочу прежний", etag)
	}
	if g.ifNone != `"etag-1"` {
		t.Errorf("If-None-Match не отправлен: %q", g.ifNone)
	}
}

func TestGitHubCheckerRateLimit(t *testing.T) {
	for _, code := range []int{http.StatusForbidden, http.StatusTooManyRequests} {
		g := newGithubSrv(t, code, `{"message":"API rate limit exceeded"}`, "", "120")
		_, _, err := g.checker().Check(context.Background())
		var rl *RateLimitError
		if !errors.As(err, &rl) {
			t.Fatalf("HTTP %d: хочу RateLimitError, получено %v", code, err)
		}
		if rl.StatusCode != code {
			t.Errorf("StatusCode = %d, хочу %d", rl.StatusCode, code)
		}
		if rl.RetryAfter != 2*time.Minute {
			t.Errorf("RetryAfter = %v, хочу 2m", rl.RetryAfter)
		}
	}
}

func TestGitHubCheckerDraft(t *testing.T) {
	g := newGithubSrv(t, http.StatusOK, `{"tag_name":"v1.4.0","draft":true}`, "", "")
	_, _, err := g.checker().Check(context.Background())
	if !errors.Is(err, ErrDraft) {
		t.Fatalf("хочу ErrDraft, получено %v", err)
	}
}

func TestGitHubCheckerBadJSON(t *testing.T) {
	g := newGithubSrv(t, http.StatusOK, `{"tag_name": `+"`", "", "")
	rel, _, err := g.checker().Check(context.Background())
	if err == nil {
		t.Fatal("битый JSON должен дать ошибку")
	}
	if rel != nil {
		t.Errorf("релиз при ошибке должен быть nil, получено %+v", rel)
	}
}

func TestGitHubCheckerServerError(t *testing.T) {
	g := newGithubSrv(t, http.StatusInternalServerError, "boom", "", "")
	_, _, err := g.checker().Check(context.Background())
	if err == nil {
		t.Fatal("HTTP 500 должен дать ошибку")
	}
	var rl *RateLimitError
	if errors.As(err, &rl) {
		t.Error("500 не должен быть RateLimitError")
	}
}
