-- «Залипающие» значения формы новой задачи, по одному ряду на пользователя
-- (актор из заголовка X-Actor). Значения — нормализованный JSON domain.TaskDefaults.
CREATE TABLE IF NOT EXISTS user_task_defaults (
    actor      TEXT PRIMARY KEY,
    payload    TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
