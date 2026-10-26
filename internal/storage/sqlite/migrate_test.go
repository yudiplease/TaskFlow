package sqlite

import (
	"database/sql"
	"fmt"
)

// OpenRawForTest открывает базу и выполняет произвольный SQL — тестовый
// хелпер для подготовки схем прошлых версий (проверка миграций).
// Живёт только в тестовой сборке пакета.
func OpenRawForTest(path, setupSQL string) (*Store, error) {
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)", path))
	if err != nil {
		return nil, fmt.Errorf("открытие sqlite (raw): %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(setupSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("setup sql: %w", err)
	}
	return &Store{db: db}, nil
}
