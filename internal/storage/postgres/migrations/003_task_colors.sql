-- Цветовая метка задачи: HEX из фиксированного справочника (см. domain.ColorTags).
-- Пустая строка = без метки. Валидация значений — в сервисном слое.
ALTER TABLE tasks ADD COLUMN color TEXT NOT NULL DEFAULT '';
