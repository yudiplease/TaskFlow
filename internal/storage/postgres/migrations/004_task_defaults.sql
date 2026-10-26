-- «Залипающие» значения формы новой задачи, по одному ряду на пользователя
-- (актор из заголовка X-Actor). Значения — нормализованный JSON domain.TaskDefaults.
CREATE TABLE IF NOT EXISTS user_task_defaults (
    actor      TEXT PRIMARY KEY,
    payload    JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
