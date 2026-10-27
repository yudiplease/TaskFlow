// Package updatechecker — MVP проверки обновлений TaskFlow через GitHub Releases.
//
// Состав:
//   - GitHubChecker — HTTP-запрос к api.github.com (ETag → 304 без тела);
//   - CachedChecker — обёртка с TTL: свежий кэш закрывает запрос без сети;
//   - StartChecker  — фоновая проверка раз в интервал, ошибки только в лог;
//   - ShouldNotify / IsNewer — чистое сравнение версий (semver).
//
// Пререлизы, ОС-тосты, авто-скачивание и проверка подписи — вне MVP.
package updatechecker

import (
	"context"
	"errors"
	"strconv"
	"time"
)

// DefaultRepo — репозиторий, в котором публикуются релизы (теги вида v1.3.2).
const DefaultRepo = "yudiplease/TaskFlow"

// Release — минимальные сведения о релизе, нужные баннеру и «Что нового».
type Release struct {
	Tag         string    `json:"tag"`                    // например v1.4.0
	Name        string    `json:"name,omitempty"`         // человекочитаемое название
	URL         string    `json:"url"`                    // html_url — открывается в браузере
	PublishedAt time.Time `json:"published_at,omitempty"` // момент публикации на GitHub
	CheckedAt   time.Time `json:"checked_at"`             // когда мы это узнали
}

// UpdateState — настройки и расписание проверки (single-row таблица update_state).
type UpdateState struct {
	Enabled        bool       `json:"enabled"`          // «Автоматически проверять»
	IntervalHours  int        `json:"interval_hours"`   // 24 | 72 | 168
	LastCheckedAt  *time.Time `json:"last_checked_at"`  // последний успешный (или 304) чек
	NextCheckAfter *time.Time `json:"next_check_after"` // пауза после rate-limit; nil — нет
}

// DefaultState — состояние при первом запуске (строки в БД ещё нет).
func DefaultState() *UpdateState {
	return &UpdateState{Enabled: true, IntervalHours: 24}
}

// ErrNotFound из хранилища означает отсутствие строки (первый запуск).
var ErrNotFound = errors.New("updatechecker: not found")

// ErrDraft — GitHub вернул черновик: пользователь его видеть не должен.
var ErrDraft = errors.New("updatechecker: релиз является черновиком")

// RateLimitError — GitHub ответил 403/429. RetryAfter — значение заголовка
// Retry-After (0, если не было); вызывающий обязан паузить
// max(RetryAfter, time.Hour), чтобы не спамить API.
type RateLimitError struct {
	StatusCode int
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return "updatechecker: лимит запросов GitHub (HTTP " + strconv.Itoa(e.StatusCode) + ")"
}

// Store — персистентность кэша релиза и состояния проверки.
// Реализации: internal/storage/sqlite (десктоп) и internal/storage/postgres.
type Store interface {
	// GetReleaseCache возвращает кэшированный релиз и ETag последнего ответа;
	// ErrNotFound, если кэша ещё нет.
	GetReleaseCache(ctx context.Context) (*Release, string, error)
	// UpsertReleaseCache сохраняет релиз атомарной транзакцией
	// (релиз + отметка времени проверки), чтобы гонки воркера и кнопки
	// «Проверить сейчас» не оставили половинную запись.
	UpsertReleaseCache(ctx context.Context, r *Release, etag string) error
	// TouchReleaseCache обновляет checked_at существующего кэша (путь 304 Not
	// Modified): время проверки свежее, данные не менялись. ErrNotFound, если кэша нет.
	TouchReleaseCache(ctx context.Context, checkedAt time.Time) error
	// GetUpdateState возвращает состояние; ErrNotFound, если строки ещё нет.
	GetUpdateState(ctx context.Context) (*UpdateState, error)
	// UpsertUpdateState сохраняет состояние целиком (upsert single-row).
	UpsertUpdateState(ctx context.Context, st *UpdateState) error
}

// Checker — источник информации о последнем релизе.
type Checker interface {
	// Check опрашивает источник. Возвращает релиз и ETag ответа;
	// (nil, etag, nil) — если данные не изменились (HTTP 304).
	Check(ctx context.Context) (*Release, string, error)
	// SetETag задаёт ETag последнего известного ответа — уйдёт в
	// If-None-Match следующего запроса.
	SetETag(etag string)
}
