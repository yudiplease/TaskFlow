package domain_test

import (
	"testing"

	"tasktracker/internal/domain"
)

func TestStatusValid(t *testing.T) {
	for _, s := range domain.Statuses {
		if !s.Valid() {
			t.Errorf("валидный статус %q отклонён", s)
		}
	}
	for _, s := range []domain.Status{"", "archived", "TODO", "in-progress"} {
		if s.Valid() {
			t.Errorf("невалидный статус %q принят", s)
		}
	}
}

func TestPriorityValid(t *testing.T) {
	for _, p := range []domain.Priority{
		domain.PriorityLow, domain.PriorityMedium, domain.PriorityHigh, domain.PriorityUrgent,
	} {
		if !p.Valid() {
			t.Errorf("валидный приоритет %q отклонён", p)
		}
	}
	for _, p := range []domain.Priority{"", "critical", "HIGH"} {
		if p.Valid() {
			t.Errorf("невалидный приоритет %q принят", p)
		}
	}
}

func TestStatusesOrder(t *testing.T) {
	want := []domain.Status{domain.StatusBacklog, domain.StatusTodo, domain.StatusInProgress, domain.StatusReview, domain.StatusDone}
	if len(domain.Statuses) != len(want) {
		t.Fatalf("число статусов = %d", len(domain.Statuses))
	}
	for i := range want {
		if domain.Statuses[i] != want[i] {
			t.Errorf("статус[%d] = %q, want %q", i, domain.Statuses[i], want[i])
		}
	}
}
