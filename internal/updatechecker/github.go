package updatechecker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const githubTimeout = 10 * time.Second

// githubRelease — поля ответа GET /repos/{owner}/{repo}/releases/latest,
// которые нам нужны. Пререлизы GitHub в /latest не отдаёт — не спрашиваем.
type githubRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
}

// GitHubChecker — реализация Checker поверх api.github.com.
// Хранит ETag последнего ответа: следующий запрос уходит с If-None-Match,
// и при 304 Not Modified GitHub не отдаёт тело (экономия лимита).
type GitHubChecker struct {
	repo string // "owner/name"
	base string // переопределяется в тестах на httptest.Server
	http *http.Client
	etag string
}

// NewGitHubChecker создаёт чекер для репозитория (пустая строка → DefaultRepo).
func NewGitHubChecker(repo string) *GitHubChecker {
	if repo == "" {
		repo = DefaultRepo
	}
	return &GitHubChecker{
		repo: repo,
		base: "https://api.github.com",
		http: &http.Client{Timeout: githubTimeout},
	}
}

// SetETag запоминает ETag последнего известного ответа.
func (g *GitHubChecker) SetETag(etag string) { g.etag = etag }

// SetBaseURL переопределяет адрес API — для тестов (httptest вместо api.github.com).
func (g *GitHubChecker) SetBaseURL(base string) { g.base = base }

// Check выполняет запрос последнего релиза.
//   - 200 → (*Release, etag, nil);
//   - 304 → (nil, прежний etag, nil);
//   - 403/429 → *RateLimitError (RetryAfter из заголовка Retry-After);
//   - прочее → ошибка. Паник нет: любые ответы читаются ограниченно и парсятся с err.
func (g *GitHubChecker) Check(ctx context.Context) (*Release, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		g.base+"/repos/"+g.repo+"/releases/latest", nil)
	if err != nil {
		return nil, "", fmt.Errorf("updatechecker: запрос: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if g.etag != "" {
		req.Header.Set("If-None-Match", g.etag)
	}

	resp, err := g.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("updatechecker: github недоступен: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotModified:
		return nil, g.etag, nil

	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return nil, "", &RateLimitError{
			StatusCode: resp.StatusCode,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}

	case resp.StatusCode != http.StatusOK:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return nil, "", fmt.Errorf("updatechecker: github ответил HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, "", fmt.Errorf("updatechecker: чтение ответа: %w", err)
	}
	var gh githubRelease
	if err := json.Unmarshal(body, &gh); err != nil {
		return nil, "", fmt.Errorf("updatechecker: ответ github не JSON: %w", err)
	}
	if gh.Draft {
		return nil, "", ErrDraft // черновик пользователь видеть не должен
	}
	if gh.TagName == "" {
		return nil, "", fmt.Errorf("updatechecker: в ответе нет tag_name")
	}

	etag := resp.Header.Get("ETag")
	if etag != "" {
		g.etag = etag
	}
	rel := &Release{
		Tag:         gh.TagName,
		Name:        gh.Name,
		URL:         gh.HTMLURL,
		PublishedAt: gh.PublishedAt,
	}
	return rel, etag, nil
}

// parseRetryAfter разбирает заголовок Retry-After (секунды). Без заголовка — 0:
// вызывающий применит минимум в час.
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	sec, err := strconv.Atoi(v)
	if err != nil || sec <= 0 {
		return 0
	}
	return time.Duration(sec) * time.Second
}
