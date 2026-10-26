package service_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"tasktracker/internal/domain"
	"tasktracker/internal/service"
)

func strPtr(s string) *string        { return &s }
func labelsPtr(l []string) *[]string { return &l }
func intPtr(v int) *int              { return &v }

func newDefaultsSvc(t *testing.T) (*service.DefaultsService, *mockDefaultsRepo) {
	t.Helper()
	repo := newMockDefaultsRepo()
	return service.NewDefaultsService(repo), repo
}

func TestDefaults_PutAndGet(t *testing.T) {
	svc, _ := newDefaultsSvc(t)
	ctx := context.Background()

	// Пока значений нет — пусто и без ошибки (форма возьмёт базовые дефолты).
	d, _, err := svc.Get(ctx, "Вы")
	if err != nil {
		t.Fatalf("Get по пустому хранилищу: %v", err)
	}
	if !d.Empty() {
		t.Errorf("ожидались пустые значения, получено %+v", d)
	}

	saved, _, err := svc.Put(ctx, "Вы", domain.TaskDefaults{Status: "in_progress", Priority: "urgent"})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, _, err := svc.Get(ctx, "Вы")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got, saved) {
		t.Errorf("Get = %+v, want %+v", got, saved)
	}

	// Значения уровня пользователя: другой актор своих значений не видит.
	other, _, err := svc.Get(ctx, "Анна")
	if err != nil {
		t.Fatalf("Get другого актора: %v", err)
	}
	if !other.Empty() {
		t.Errorf("у другого актора должны быть свои пустые значения, получено %+v", other)
	}
}

func TestDefaults_PutValidates(t *testing.T) {
	svc, _ := newDefaultsSvc(t)
	if _, _, err := svc.Put(context.Background(), "Вы", domain.TaskDefaults{Status: "archived"}); err == nil {
		t.Error("невалидный статус должен возвращать ошибку")
	}
	if _, _, err := svc.Put(context.Background(), "Вы", domain.TaskDefaults{Color: "#FF0000"}); err == nil {
		t.Error("чужой цвет должен возвращать ошибку")
	}
}

func TestDefaults_ApplyPatch_OnlyPresentFields(t *testing.T) {
	svc, _ := newDefaultsSvc(t)
	ctx := context.Background()

	if _, _, err := svc.ApplyPatch(ctx, "Вы", service.StickyPatch{
		Status:   strPtr("in_progress"),
		Priority: strPtr("urgent"),
		Assignee: strPtr("Анна"),
	}); err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	// PATCH меняет только приоритет — статус и исполнитель не перетираются.
	if _, _, err := svc.ApplyPatch(ctx, "Вы", service.StickyPatch{
		Priority: strPtr("low"),
	}); err != nil {
		t.Fatalf("ApplyPatch (только приоритет): %v", err)
	}
	d, _, _ := svc.Get(ctx, "Вы")
	if d.Status != "in_progress" || d.Assignee != "Анна" {
		t.Errorf("отсутствующие в патче поля изменились: %+v", d)
	}
	if d.Priority != "low" {
		t.Errorf("приоритет = %q, want low", d.Priority)
	}
}

func TestDefaults_ApplyPatch_EmptyClears(t *testing.T) {
	svc, _ := newDefaultsSvc(t)
	ctx := context.Background()

	_, _, err := svc.ApplyPatch(ctx, "Вы", service.StickyPatch{
		Status:   strPtr("review"),
		Assignee: strPtr("Михаил"),
		Labels:   labelsPtr([]string{"api", "тесты"}),
	})
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	// Пустая строка/срез в присутствовавшем поле — явный сброс запомненного.
	if _, _, err := svc.ApplyPatch(ctx, "Вы", service.StickyPatch{
		Assignee: strPtr("   "),
		Labels:   labelsPtr([]string{}),
	}); err != nil {
		t.Fatalf("ApplyPatch (сброс): %v", err)
	}
	d, _, _ := svc.Get(ctx, "Вы")
	if d.Assignee != "" || d.Labels != nil {
		t.Errorf("сброс не сработал: %+v", d)
	}
	if d.Status != "review" {
		t.Errorf("статус не должен был измениться: %+v", d)
	}
}

func TestDefaults_ApplyPatch_EmptyPatchIsNoop(t *testing.T) {
	svc, _ := newDefaultsSvc(t)
	ctx := context.Background()

	if _, _, err := svc.ApplyPatch(ctx, "Вы", service.StickyPatch{}); err != nil {
		t.Fatalf("ApplyPatch пустого патча: %v", err)
	}
	d, _, _ := svc.Get(ctx, "Вы")
	if !d.Empty() {
		t.Errorf("пустой патч не должен ничего записывать: %+v", d)
	}
}

func TestDefaults_ApplyPatch_HealsCorruptedStoredValues(t *testing.T) {
	svc, repo := newDefaultsSvc(t)
	ctx := context.Background()

	// Порченая запись (ручная правка БД): статус невалиден.
	repo.items["Вы"] = domain.TaskDefaults{Status: "archived", Priority: "urgent"}

	if _, _, err := svc.ApplyPatch(ctx, "Вы", service.StickyPatch{Priority: strPtr("low")}); err != nil {
		t.Fatalf("ApplyPatch поверх испорченной записи: %v", err)
	}
	d, _, _ := svc.Get(ctx, "Вы")
	// Невалидные значения отбрасываются, патч сохраняется.
	if d.Status != "" || d.Priority != "low" {
		t.Errorf("испорченная запись не исцелена: %+v", d)
	}
}

func TestDefaults_ApplyPatch_PerUser(t *testing.T) {
	svc, _ := newDefaultsSvc(t)
	ctx := context.Background()

	if _, _, err := svc.ApplyPatch(ctx, "Вы", service.StickyPatch{Status: strPtr("in_progress")}); err != nil {
		t.Fatalf("ApplyPatch Вы: %v", err)
	}
	if _, _, err := svc.ApplyPatch(ctx, "Анна", service.StickyPatch{Status: strPtr("done")}); err != nil {
		t.Fatalf("ApplyPatch Анна: %v", err)
	}
	mine, _, _ := svc.Get(ctx, "Вы")
	annas, _, _ := svc.Get(ctx, "Анна")
	if mine.Status != "in_progress" || annas.Status != "done" {
		t.Errorf("значения перемешались между пользователями: %q / %q", mine.Status, annas.Status)
	}
}

func TestDefaults_ApplyPatch_TimeOfDayRemembered(t *testing.T) {
	svc, _ := newDefaultsSvc(t)
	ctx := context.Background()

	// Из задачи со сроком 18:30 запоминается только ВРЕМЯ, не дата:
	// в sticky-структуре нет поля для абсолютной даты по построению.
	if _, _, err := svc.ApplyPatch(ctx, "Вы", service.StickyPatch{
		DueTimeOfDay:          strPtr("18:30"),
		ReminderOffsetMinutes: intPtr(15),
	}); err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	d, _, _ := svc.Get(ctx, "Вы")
	if d.DueTimeOfDay != "18:30" {
		t.Errorf("due_time_of_day = %q, want 18:30", d.DueTimeOfDay)
	}
	if d.ReminderOffsetMinutes != 15 {
		t.Errorf("reminder_offset_minutes = %d, want 15", d.ReminderOffsetMinutes)
	}
	raw, _ := json.Marshal(d)
	if strings.Contains(string(raw), "due_at") || strings.Contains(string(raw), "date") {
		t.Errorf("в sticky-значениях не должно быть абсолютной даты: %s", raw)
	}

	// All-day или сброс срока (пустая строка) сбрасывают запомненное время,
	// напоминание при этом остаётся.
	if _, _, err := svc.ApplyPatch(ctx, "Вы", service.StickyPatch{
		DueTimeOfDay: strPtr(""),
	}); err != nil {
		t.Fatalf("ApplyPatch (сброс времени): %v", err)
	}
	d, _, _ = svc.Get(ctx, "Вы")
	if d.DueTimeOfDay != "" || d.ReminderOffsetMinutes != 15 {
		t.Errorf("после сброса времени: %+v", d)
	}

	// «Без напоминания» (<=0) сбрасывает запомненное.
	if _, _, err := svc.ApplyPatch(ctx, "Вы", service.StickyPatch{
		ReminderOffsetMinutes: intPtr(0),
	}); err != nil {
		t.Fatalf("ApplyPatch (сброс напоминания): %v", err)
	}
	d, _, _ = svc.Get(ctx, "Вы")
	if d.ReminderOffsetMinutes != 0 {
		t.Errorf("напоминание должно быть сброшено: %+v", d)
	}
}

func TestDefaults_ApplyPatch_TimeOfDayNormalized(t *testing.T) {
	svc, _ := newDefaultsSvc(t)
	ctx := context.Background()

	// «9:5» от фронтенда канонизируется в «09:05» при сохранении.
	if _, _, err := svc.ApplyPatch(ctx, "Вы", service.StickyPatch{DueTimeOfDay: strPtr("9:5")}); err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	d, _, _ := svc.Get(ctx, "Вы")
	if d.DueTimeOfDay != "09:05" {
		t.Errorf("due_time_of_day = %q, want 09:05", d.DueTimeOfDay)
	}
}
