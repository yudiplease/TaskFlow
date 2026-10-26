package domain

import (
	"fmt"
	"strings"
)

// StickyFields — какие поля задачи «залипают» между созданиями.
// Осознанно исключены поля, привязанные к конкретной задаче:
// title, description, id, timestamps и АБСОЛЮТНАЯ дата срока (due_at) —
// иначе каждая новая задача получала бы вчерашний/просроченный срок.
// Вместо даты залипает только время суток (DueTimeOfDay).
var StickyFields = []string{"status", "priority", "color", "assignee", "labels", "dueTimeOfDay", "reminderOffsetMinutes"}

// TaskDefaults — «залипающие» значения для предзаполнения формы новой задачи.
// Пустые поля означают «нет запомненного значения» — форма возьмёт базовый дефолт.
type TaskDefaults struct {
	Status   string   `json:"status,omitempty"`
	Priority string   `json:"priority,omitempty"`
	Color    string   `json:"color,omitempty"`
	Assignee string   `json:"assignee,omitempty"`
	Labels   []string `json:"labels,omitempty"`
	// DueTimeOfDay — «ЧЧ:ММ» из последней задачи: при открытии новой формы
	// подставляется на сегодня (или завтра, если это время сегодня уже
	// прошло — правило фронтенда), но НИКОГДА как абсолютная дата.
	DueTimeOfDay string `json:"due_time_of_day,omitempty"`
	// ReminderOffsetMinutes — запомненное «напомнить за N минут»; 0 = не запомнено.
	ReminderOffsetMinutes int `json:"reminder_offset_minutes,omitempty"`
}

// Empty возвращает true, если ни одно sticky-поле не заполнено.
func (d TaskDefaults) Empty() bool {
	return d.Status == "" && d.Priority == "" && d.Color == "" && d.Assignee == "" &&
		len(d.Labels) == 0 && d.DueTimeOfDay == "" && d.ReminderOffsetMinutes == 0
}

// Normalize валидирует и приводит значения к канонической форме.
// Неизвестные статусы/приоритеты/цвета — ошибка, а не тихое игнорирование:
// некорректный дефолт испортил бы каждую новую задачу.
func (d TaskDefaults) Normalize() (TaskDefaults, error) {
	out := TaskDefaults{}
	if d.Status != "" {
		st := Status(d.Status)
		if !st.Valid() {
			return out, fmt.Errorf("%w: неизвестный статус %q", ErrInvalid, d.Status)
		}
		out.Status = string(st)
	}
	if d.Priority != "" {
		p := Priority(d.Priority)
		if !p.Valid() {
			return out, fmt.Errorf("%w: неизвестный приоритет %q", ErrInvalid, d.Priority)
		}
		out.Priority = string(p)
	}
	color, err := NormalizeColor(d.Color)
	if err != nil {
		return out, err
	}
	out.Color = color
	out.Assignee = strings.TrimSpace(d.Assignee)
	if len(out.Assignee) > 100 {
		return out, fmt.Errorf("%w: исполнитель длиннее 100 символов", ErrInvalid)
	}
	labels, err := normalizeDefaultsLabels(d.Labels)
	if err != nil {
		return out, err
	}
	out.Labels = labels
	if d.DueTimeOfDay != "" {
		minutes, err := ParseTimeOfDay(d.DueTimeOfDay)
		if err != nil {
			return out, err
		}
		out.DueTimeOfDay = FormatTimeOfDay(minutes)
	}
	if d.ReminderOffsetMinutes != 0 {
		if d.ReminderOffsetMinutes < 0 || d.ReminderOffsetMinutes > MaxReminderOffsetMinutes {
			return out, fmt.Errorf("%w: напоминание должно быть от 1 до %d минут", ErrInvalid, MaxReminderOffsetMinutes)
		}
		out.ReminderOffsetMinutes = d.ReminderOffsetMinutes
	}
	return out, nil
}

func normalizeDefaultsLabels(labels []string) ([]string, error) {
	if len(labels) > 10 {
		return nil, fmt.Errorf("%w: не больше 10 меток", ErrInvalid)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if len(l) > 30 {
			return nil, fmt.Errorf("%w: метка длиннее 30 символов", ErrInvalid)
		}
		low := strings.ToLower(l)
		if !seen[low] {
			seen[low] = true
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
