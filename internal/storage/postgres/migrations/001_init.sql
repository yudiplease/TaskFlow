CREATE TABLE IF NOT EXISTS tasks (
    id          UUID PRIMARY KEY,
    title       TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    status      TEXT        NOT NULL DEFAULT 'backlog',
    priority    TEXT        NOT NULL DEFAULT 'medium',
    assignee    TEXT        NOT NULL DEFAULT '',
    labels      TEXT[]      NOT NULL DEFAULT '{}',
    due_date    TIMESTAMPTZ NULL,
    position    DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_tasks_status   ON tasks (status);
CREATE INDEX IF NOT EXISTS idx_tasks_priority ON tasks (priority);
CREATE INDEX IF NOT EXISTS idx_tasks_assignee ON tasks (assignee);
CREATE INDEX IF NOT EXISTS idx_tasks_position ON tasks (status, position);

CREATE TABLE IF NOT EXISTS activity (
    id         BIGSERIAL PRIMARY KEY,
    task_id    UUID        NULL,
    type       TEXT        NOT NULL,
    actor      TEXT        NOT NULL DEFAULT '',
    payload    JSONB       NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_activity_task   ON activity (task_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_activity_recent ON activity (id DESC);
