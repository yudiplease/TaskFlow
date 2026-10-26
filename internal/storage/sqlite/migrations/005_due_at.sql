-- Срок задачи с точностью до минуты: due_date → due_at (TEXT, RFC3339-инстанты в UTC).
-- Новые поля: due_all_day (0/1), reminder_offset_minutes, reminder_sent_at, due_timezone.
-- БИЗНЕС-ПРАВИЛО миграции: старые сроки = «конец дня» (23:59 локального времени,
-- так их собирал старый фронтенд) → все существующие строки получают
-- due_all_day = 1; инстанты сохраняются без изменений. Откат: RENAME обратно
-- и DROP добавленных колонок.

ALTER TABLE tasks RENAME COLUMN due_date TO due_at;

ALTER TABLE tasks ADD COLUMN due_all_day INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN reminder_offset_minutes INTEGER NULL;
ALTER TABLE tasks ADD COLUMN reminder_sent_at TEXT NULL;
ALTER TABLE tasks ADD COLUMN due_timezone TEXT NOT NULL DEFAULT '';

UPDATE tasks SET due_all_day = 1 WHERE due_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_tasks_due_at ON tasks (due_at) WHERE due_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_tasks_reminder ON tasks (reminder_sent_at) WHERE reminder_sent_at IS NULL;
