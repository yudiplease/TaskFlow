package updatechecker

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// StartChecker — фоновая проверка обновлений раз в интервал.
//
// Вызывается из main как отдельная горутина (НЕ через supervisor.Go):
// у функции есть легитимные тихие выходы — dev-сборка и «проверяли недавно»,
// а supervisor считает быстрый возврат воркера падением и уходит в
// рестарт-цикл с ошибками в логе. Паник здесь нет: все операции —
// проверенные ошибки, никакой log.Fatal.
//
// Поведение на каждом тике:
//   - version не парсится как semver ("dev") → проверки нет вовсе (dev-guard);
//   - авто-проверка выключена пользователем → тихо;
//   - now < next_check_after (пауза после rate-limit) → тихо;
//   - now - last_checked_at < interval → тихо (выход = пропуск проверки,
//     горутина засыпает до следующего тика, чтобы не крутить цикл впустую);
//   - иначе проверка; при ошибке — только log.Warn, приложение продолжает жить.
func StartChecker(ctx context.Context, store Store, appVersion string, defaultInterval time.Duration, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	if defaultInterval <= 0 {
		defaultInterval = DefaultInterval
	}
	if !IsValidVersion(appVersion) {
		log.Info("проверка обновлений отключена: версия сборки не semver", "version", appVersion)
		return
	}

	checker := NewCachedChecker(NewGitHubChecker(DefaultRepo), store, defaultInterval, log)

	for {
		st, err := store.GetUpdateState(ctx)
		if err != nil && !errors.Is(err, ErrNotFound) {
			log.Warn("чтение состояния обновлений", "err", err)
			st = nil
		}
		interval := effectiveInterval(st, defaultInterval)
		if err := checkDue(st, interval, time.Now()); err == nil {
			runOnce(ctx, checker, appVersion, st, log)
		}

		// Спим до следующего тика; выход — только по отмене контекста.
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// checkDue — чистое условие «пора ли проверять» (вынесено для тестов):
// включено, не в паузе после rate-limit, и прошло не меньше интервала
// с последней проверки.
func checkDue(st *UpdateState, interval time.Duration, now time.Time) error {
	if st == nil {
		return nil // первый запуск: строки state ещё нет — проверяем с дефолтами
	}
	if !st.Enabled {
		return errors.New("авто-проверка выключена")
	}
	if st.NextCheckAfter != nil && now.Before(*st.NextCheckAfter) {
		return errors.New("пауза после rate-limit")
	}
	if st.LastCheckedAt != nil && now.Sub(*st.LastCheckedAt) < interval {
		return errors.New("проверяли недавно")
	}
	return nil
}

func effectiveInterval(st *UpdateState, fallback time.Duration) time.Duration {
	if st != nil && st.IntervalHours > 0 {
		return time.Duration(st.IntervalHours) * time.Hour
	}
	return fallback
}

// runOnce — одна тихая проверка: успех пишется в кэш самим CachedChecker,
// ошибка остаётся только в логе. Пользователю здесь показывать нечего:
// баннер UI увидит, прочитав /api/updates/state.
func runOnce(ctx context.Context, checker *CachedChecker, appVersion string, st *UpdateState, log *slog.Logger) {
	rel, err := checker.Check(ctx) // TTL-обёртка: если кэш почему-то свежий — сеть не тронется
	if err != nil {
		// Единственное место, где ошибка фона видна, — лог. Rate-limit уже
		// записал next_check_after внутрь CachedChecker.
		log.Warn("фоновая проверка обновлений не удалась", "err", err)
		return
	}
	if rel == nil {
		return
	}
	if st == nil {
		st = DefaultState() // самый первый чек: строки state ещё нет
	}
	if !IsValidVersion(rel.Tag) {
		// Тег не semver — молчим в UI, но в лог сообщаем.
		log.Warn("GitHub вернул тег, не парсящийся как semver", "tag", rel.Tag)
		return
	}
	if ShouldNotify(rel.Tag, appVersion, st) {
		log.Info("доступна новая версия TaskFlow", "latest", rel.Tag, "current", appVersion)
	}
}
