// Package sqlite — встроенное хранилище десктопной версии (modernc.org/sqlite,
// чистый Go без CGO). Реализует те же интерфейсы, что и postgres-версия.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Store — обёртка над *sql.DB с общими кодеками.
type Store struct {
	db *sql.DB
}

// Open открывает базу (file — путь к файлу; ":memory:" для тестов),
// применяет миграции и настраивает прагмы под однопользовательский режим.
func Open(ctx context.Context, file string, log *slog.Logger) (*Store, error) {
	// Один файл, один процесс: WAL + busy_timeout делают запись устойчивой
	// к редким конкурентным доступам без "database is locked".
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", file)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("открытие sqlite: %w", err)
	}
	// SQLite не любит параллельные писатели — ограничиваем соединения.
	db.SetMaxOpenConns(1)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, fmt.Errorf("пинг sqlite: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(ctx, log); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close закрывает базу.
func (s *Store) Close() error { return s.db.Close() }

// Ping проверяет доступность базы (для readiness-пробы).
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) migrate(ctx context.Context, log *slog.Logger) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("создание таблицы миграций: %w", err)
	}

	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("чтение миграций: %w", err)
	}

	for _, e := range entries {
		name := e.Name()
		var exists bool
		if err := s.db.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name = $1)`, name,
		).Scan(&exists); err != nil {
			return fmt.Errorf("проверка миграции %s: %w", name, err)
		}
		if exists {
			continue
		}

		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("чтение миграции %s: %w", name, err)
		}

		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("начало транзакции: %w", err)
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("применение миграции %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (name, applied_at) VALUES ($1, $2)`,
			name, formatTime(time.Now().UTC())); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("запись миграции %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("фиксация миграции %s: %w", name, err)
		}
		log.Info("миграция применена", "engine", "sqlite", "name", name)
	}
	return nil
}

// formatTime/parseTime — единый текстовый формат дат в SQLite.
func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s sql.NullString) (time.Time, error) {
	if !s.Valid || s.String == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, s.String)
}

func parseTimePtr(s sql.NullString) (*time.Time, error) {
	if !s.Valid || s.String == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, s.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func formatLabels(labels []string) string {
	if labels == nil {
		labels = []string{}
	}
	b, _ := json.Marshal(labels)
	return string(b)
}

func parseLabels(s string) []string {
	out := []string{}
	if s != "" {
		_ = json.Unmarshal([]byte(s), &out)
	}
	return out
}
