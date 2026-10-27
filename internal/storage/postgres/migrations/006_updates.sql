-- Проверка обновлений через GitHub Releases (MVP).
-- release_cache — кэш последнего релиза (single-row: CHECK (id = 1));
-- update_state  — настройки авто-проверки и расписание (single-row).

CREATE TABLE IF NOT EXISTS release_cache (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    tag_name TEXT NOT NULL,
    release_name TEXT NOT NULL DEFAULT '',
    html_url TEXT NOT NULL DEFAULT '',
    published_at TIMESTAMPTZ NULL,
    etag TEXT NOT NULL DEFAULT '',
    checked_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS update_state (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    interval_hours INTEGER NOT NULL DEFAULT 24,
    last_checked_at TIMESTAMPTZ NULL,
    next_check_after TIMESTAMPTZ NULL
);
