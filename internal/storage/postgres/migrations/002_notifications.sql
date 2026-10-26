CREATE TABLE IF NOT EXISTS notifications (
    id         BIGSERIAL PRIMARY KEY,
    task_id    UUID        NOT NULL,
    type       TEXT        NOT NULL,
    title      TEXT        NOT NULL,
    message    TEXT        NOT NULL,
    dedupe_key TEXT        NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    read_at    TIMESTAMPTZ NULL
);

-- Одно напоминание на задачу/тип/день, unread-индекс для бейджа.
CREATE INDEX IF NOT EXISTS idx_notifications_unread ON notifications (id DESC) WHERE read_at IS NULL;
