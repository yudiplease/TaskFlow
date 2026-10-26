// Package config загружает настройки приложения из переменных окружения.
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config — конфигурация сервера.
type Config struct {
	Port            int
	PostgresDSN     string
	KafkaBrokers    []string
	KafkaTopic      string
	KafkaGroup      string
	KafkaEnabled    bool
	ShutdownTimeout time.Duration
}

// Load читает конфигурацию, подставляя разумные значения по умолчанию.
func Load() Config {
	return Config{
		Port:            envInt("PORT", 8080),
		PostgresDSN:     envStr("POSTGRES_DSN", "postgres://tasktracker:tasktracker@localhost:5432/tasktracker?sslmode=disable"),
		KafkaBrokers:    splitList(envStr("KAFKA_BROKERS", "localhost:29092")),
		KafkaTopic:      envStr("KAFKA_TOPIC", "task-events"),
		KafkaGroup:      envStr("KAFKA_GROUP", "activity-writer"),
		KafkaEnabled:    envBool("KAFKA_ENABLED", true),
		ShutdownTimeout: 10 * time.Second,
	}
}

func envStr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func splitList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
