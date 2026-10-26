-- Срок задачи с точностью до минуты: due_date → due_at (timestamptz, инстанты в UTC).
-- Новые поля:
--   due_all_day              — «весь день»: время в UI скрыто, инстант = 23:59 локального дня.
--   reminder_offset_minutes  — за сколько минут до срока напомнить (NULL = не напоминать).
--   reminder_sent_at         — когда отправлено персональное напоминание (идемпотентность).
--   due_timezone             — IANA-зона на момент постановки (граница «дня» all-day задач).
--
-- БИЗНЕС-ПРАВИЛО миграции: старый фронтенд отправлял только дату и фиксировал
-- конец дня (23:59 локального времени пользователя), поэтому все существующие
-- строки помечаются due_all_day = TRUE — их инстанты (23:59 локального дня)
-- сохраняются без изменений, время в UI не показывается. (Альтернатива 00:00
-- отвергнута: она делала бы задачу «просроченной» весь её последний день.)
--
-- ОТКАТ:
--   ALTER TABLE tasks RENAME COLUMN due_at TO due_date;
--   ALTER TABLE tasks DROP COLUMN due_all_day;
--   ALTER TABLE tasks DROP COLUMN reminder_offset_minutes;
--   ALTER TABLE tasks DROP COLUMN reminder_sent_at;
--   ALTER TABLE tasks DROP COLUMN due_timezone;

ALTER TABLE tasks RENAME COLUMN due_date TO due_at;

ALTER TABLE tasks ADD COLUMN due_all_day BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE tasks ADD COLUMN reminder_offset_minutes INT NULL;
ALTER TABLE tasks ADD COLUMN reminder_sent_at TIMESTAMPTZ NULL;
ALTER TABLE tasks ADD COLUMN due_timezone TEXT NOT NULL DEFAULT '';

-- Существующие сроки поставлены старым клиентом (только дата, конец дня).
UPDATE tasks SET due_all_day = TRUE WHERE due_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_tasks_due_at ON tasks (due_at) WHERE due_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_tasks_reminder ON tasks (reminder_sent_at) WHERE reminder_sent_at IS NULL;
