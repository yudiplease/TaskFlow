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
}

func TestTaskDefaults_Normalize_OK(t *testing.T) {
	d, err := TaskDefaults{
		Status:   "in_progress",
		Priority: "urgent",
		Color:    " #e53935 ",
		Assignee: "  Анна  ",
		Labels:   []string{" devops ", "DevOps", "", "api"},
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
}

func TestTaskDefaults_Normalize_EmptyStaysEmpty(t *testing.T) {
	// Пустая строка = «нет запомненного значения», а не ошибка.
	d, err := TaskDefaults{Status: "", Priority: "", Color: "   ", Assignee: "", Labels: nil}.Normalize()
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
	for _, forbidden := range []string{"title", "description", "due_date", "id"} {
		for _, f := range StickyFields {
			if f == forbidden {
				t.Errorf("поле %q не должно «залипать» — оно привязано к конкретной задаче", f)
			}
		}
	}
}
