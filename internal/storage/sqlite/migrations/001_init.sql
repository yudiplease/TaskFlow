CREATE TABLE IF NOT EXISTS tasks (
    id          TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'backlog',
    priority    TEXT NOT NULL DEFAULT 'medium',
    assignee    TEXT NOT NULL DEFAULT '',
    labels      TEXT NOT NULL DEFAULT '[]',
    due_date    TEXT,
    position    REAL NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tasks_status   ON tasks (status);
CREATE INDEX IF NOT EXISTS idx_tasks_priority ON tasks (priority);
CREATE INDEX IF NOT EXISTS idx_tasks_assignee ON tasks (assignee);
CREATE INDEX IF NOT EXISTS idx_tasks_position ON tasks (status, position);

CREATE TABLE IF NOT EXISTS activity (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id    TEXT,
    type       TEXT NOT NULL,
    actor      TEXT NOT NULL DEFAULT '',
    payload    TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_activity_task   ON activity (task_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_activity_recent ON activity (id DESC);
