CREATE TABLE IF NOT EXISTS notifications (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id    TEXT NOT NULL,
    type       TEXT NOT NULL,
    title      TEXT NOT NULL,
    message    TEXT NOT NULL,
    dedupe_key TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL,
    read_at    TEXT
);

CREATE INDEX IF NOT EXISTS idx_notifications_unread ON notifications (id DESC) WHERE read_at IS NULL;
