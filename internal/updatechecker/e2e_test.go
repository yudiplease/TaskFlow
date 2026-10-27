package updatechecker_test

// Сквозной MVP-путь без реального GitHub: httptest.Server подменяет
// api.github.com, хранилище — SQLite in-memory с настоящими миграциями.
// Путь: CachedChecker (реальный GitHubChecker) → Store → Status →
// решение «баннер показать».

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"tasktracker/internal/storage/sqlite"
	"tasktracker/internal/updatechecker"
)

const e2eReleaseJSON = `{
  "tag_name": "v1.4.0",
  "name": "TaskFlow v1.4.0",
  "draft": false,
  "html_url": "https://github.com/yudiplease/TaskFlow/releases/tag/v1.4.0",
  "published_at": "2026-10-20T12:00:00Z"
}`

func TestEndToEndCheckToStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1.4.0"`)
		w.Write([]byte(e2eReleaseJSON))
	}))
	defer srv.Close()

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	store, err := sqlite.Open(ctx, ":memory:", log)
	if err != nil {
		t.Fatalf("sqlite in-memory: %v", err)
	}
	defer store.Close()

	gh := updatechecker.NewGitHubChecker(updatechecker.DefaultRepo)
	gh.SetBaseURL(srv.URL) // подмена base для тестов

	checker := updatechecker.NewCachedChecker(gh, sqlite.NewUpdates(store), updatechecker.DefaultInterval, log)

	// 1) Принудительная проверка: HTTP 200 → релиз в кэше.
	rel, err := checker.CheckNow(ctx)
	if err != nil {
		t.Fatalf("CheckNow: %v", err)
	}
	if rel == nil || rel.Tag != "v1.4.0" {
		t.Fatalf("релиз из httptest не дошёл: %+v", rel)
	}

	// 1a) На чистой БД строки update_state ещё нет — UpsertReleaseCache
	// обязан создать её сам, иначе «Последняя проверка» вечно «ещё не было».
	st0, err := checker.Status(ctx, "v1.3.2")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st0.LastCheckedAt == nil {
		t.Fatal("last_checked_at не записан при первой проверке на чистой БД")
	}

	// 2) Статус для UI: баннер должен быть показан.
	st, err := checker.Status(ctx, "v1.3.2")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.UpdateAvailable || st.Latest == nil || st.Latest.Tag != "v1.4.0" {
		t.Fatalf("баннер не должен молчать: %+v", st)
	}

	// 3) Повторная проверка с тем же ETag: GitHub отвечает 304 —
	//    latest_tag прежний, время проверки продлено.
	etagRel, err := checker.CheckNow(ctx)
	if err != nil {
		t.Fatalf("повторный CheckNow (304): %v", err)
	}
	if etagRel == nil || etagRel.Tag != "v1.4.0" {
		t.Fatalf("после 304 релиз потерялся: %+v", etagRel)
	}

	// 4) Сохранение настроек: чекбокс и интервал едут через тот же store.
	if err := checker.SetSettings(ctx, false, 72); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}
	st2, err := checker.Status(ctx, "v1.3.2")
	if err != nil {
		t.Fatalf("Status после SetSettings: %v", err)
	}
	if st2.Enabled || st2.IntervalHours != 72 {
		t.Errorf("настройки не применились: %+v", st2)
	}
	if st2.UpdateAvailable {
		t.Error("при выключенной авто-проверке баннер фоном не показывается")
	}
}

// Dev-сборка: ShouldNotify молчит даже при свежем кэше с новой версией.
func TestEndToEndDevBuildSilent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(e2eReleaseJSON))
	}))
	defer srv.Close()

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	store, err := sqlite.Open(ctx, ":memory:", log)
	if err != nil {
		t.Fatalf("sqlite in-memory: %v", err)
	}
	defer store.Close()

	gh := updatechecker.NewGitHubChecker(updatechecker.DefaultRepo)
	gh.SetBaseURL(srv.URL)
	checker := updatechecker.NewCachedChecker(gh, sqlite.NewUpdates(store), updatechecker.DefaultInterval, log)

	if _, err := checker.CheckNow(ctx); err != nil {
		t.Fatalf("CheckNow: %v", err)
	}
	st, err := checker.Status(ctx, "dev")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.UpdateAvailable {
		t.Error("на dev-сборке баннер показываться не должен")
	}
}
