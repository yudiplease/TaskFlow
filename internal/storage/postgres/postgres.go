// Package postgres — реализация хранилища на PostgreSQL (pgx).
package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"tasktracker/internal/config"
)

// New создаёт пул соединений и применяет миграции.
func New(ctx context.Context, cfg config.Config, log *slog.Logger) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, cfg.PostgresDSN)
	if err != nil {
		return nil, fmt.Errorf("создание пула: %w", err)
	}

	pool.Config().MaxConns = 10
	pool.Config().MaxConnLifetime = time.Hour

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("подключение к postgres: %w", err)
	}

	if err := migrate(ctx, pool, log); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}
