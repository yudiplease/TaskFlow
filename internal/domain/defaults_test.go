package domain

import (
	"errors"
	"testing"
)

func TestTaskDefaults_Empty(t *testing.T) {
	if !(TaskDefaults{}).Empty() {
		t.Error("пустые значения не должны считаться заполненными")
	}
	if (TaskDefaults{Priority: "high"}).Empty() {
		t.Error("заполненный приоритет должен делать структуру непустой")
	}
	if (TaskDefaults{Labels: []string{"api"}}).Empty() {
		t.Error("метки должны делать структуру непустой")
	}
	if (TaskDefaults{DueTimeOfDay: "18:30"}).Empty() {
		t.Error("запомненное время срока должно делать структуру непустой")
	}
	if (TaskDefaults{ReminderOffsetMinutes: 15}).Empty() {
		t.Error("запомненное напоминание должно делать структуру непустой")
	}
}

func TestTaskDefaults_Normalize_OK(t *testing.T) {
	d, err := TaskDefaults{
		Status:                "in_progress",
		Priority:              "urgent",
		Color:                 " #e53935 ",
		Assignee:              "  Анна  ",
		Labels:                []string{" devops ", "DevOps", "", "api"},
		DueTimeOfDay:          "9:5",
		ReminderOffsetMinutes: 15,
	}.Normalize()
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if d.Status != "in_progress" || d.Priority != "urgent" {
		t.Errorf("статус/приоритет = %q/%q", d.Status, d.Priority)
	}
	if d.Color != "#E53935" {
		t.Errorf("цвет = %q, want #E53935", d.Color)
	}
	if d.Assignee != "Анна" {
		t.Errorf("исполнитель = %q, want Анна", d.Assignee)
	}
	if len(d.Labels) != 2 || d.Labels[0] != "devops" || d.Labels[1] != "api" {
		t.Errorf("метки = %v, want [devops api]", d.Labels)
	}
	// Время суток канонизируется; дата в sticky не хранится в принципе —
	// в структуре просто нет поля для неё.
	if d.DueTimeOfDay != "09:05" {
		t.Errorf("due_time_of_day = %q, want 09:05", d.DueTimeOfDay)
	}
	if d.ReminderOffsetMinutes != 15 {
		t.Errorf("reminder_offset_minutes = %d, want 15", d.ReminderOffsetMinutes)
	}
}

func TestTaskDefaults_Normalize_EmptyStaysEmpty(t *testing.T) {
	// Пустая строка = «нет запомненного значения», а не ошибка.
	d, err := TaskDefaults{Status: "", Priority: "", Color: "   ", Assignee: "", Labels: nil, DueTimeOfDay: ""}.Normalize()
	if err != nil {
		t.Fatalf("Normalize пустых значений: %v", err)
	}
	if !d.Empty() {
		t.Errorf("после нормализации должно остаться пусто, получено %+v", d)
	}
}

func TestTaskDefaults_Normalize_Rejects(t *testing.T) {
	cases := []struct {
		name string
		in   TaskDefaults
	}{
		{"неизвестный статус", TaskDefaults{Status: "archived"}},
		{"неизвестный приоритет", TaskDefaults{Priority: "critical"}},
		{"чужой цвет", TaskDefaults{Color: "#FF0000"}},
		{"длинный исполнитель", TaskDefaults{Assignee: string(make([]byte, 101))}},
		{"много меток", TaskDefaults{Labels: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"}}},
		{"длинная метка", TaskDefaults{Labels: []string{string(make([]byte, 31))}}},
		{"невозможное время", TaskDefaults{DueTimeOfDay: "24:00"}},
		{"минуты вне диапазона", TaskDefaults{DueTimeOfDay: "12:75"}},
		{"отрицательное напоминание", TaskDefaults{ReminderOffsetMinutes: -10}},
		{"напоминание за год", TaskDefaults{ReminderOffsetMinutes: 100000}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.in.Normalize()
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("ожидалась ErrInvalid, получено %v", err)
			}
		})
	}
}

func TestStickyFields_ExcludesTaskBoundValues(t *testing.T) {
	for _, forbidden := range []string{"title", "description", "due_date", "due_at", "id"} {
		for _, f := range StickyFields {
			if f == forbidden {
				t.Errorf("поле %q не должно «залипать» — оно привязано к конкретной задаче", f)
			}
		}
	}
	// Время суток залипает, абсолютная дата — нет.
	var hasTimeOfDay bool
	for _, f := range StickyFields {
		if f == "dueTimeOfDay" {
			hasTimeOfDay = true
		}
	}
	if !hasTimeOfDay {
		t.Error("dueTimeOfDay должен быть в списке sticky-полей")
	}
}
