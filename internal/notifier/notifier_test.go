package notifier_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"tasktracker/internal/domain"
	"tasktracker/internal/events"
	"tasktracker/internal/metrics"
	"tasktracker/internal/notifier"
	"tasktracker/internal/storage"
)

// taskRepo — память-репозиторий с фильтром ListDue.
type taskRepo struct {
	mu    sync.Mutex
	tasks []domain.Task
}

func (r *taskRepo) ListDue(_ context.Context, notAfter time.Time) ([]domain.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.Task
	for _, t := range r.tasks {
		if t.Status != domain.StatusDone && t.DueAt != nil && !t.DueAt.After(notAfter) {
			out = append(out, t)
		}
	}
	return out, nil
}

// Неиспользуемые методы интерфейса — заглушки.
func (r *taskRepo) Create(context.Context, *domain.Task) error { return nil }
func (r *taskRepo) Get(context.Context, string) (*domain.Task, error) {
	return nil, storage.ErrNotFound
}
func (r *taskRepo) Update(context.Context, *domain.Task) error { return nil }
func (r *taskRepo) Delete(context.Context, string) error       { return nil }
func (r *taskRepo) List(context.Context, domain.TaskFilter) ([]domain.Task, int, error) {
	return nil, 0, nil
}
func (r *taskRepo) MaxPosition(context.Context, domain.Status) (float64, error) {
	return 0, nil
}
func (r *taskRepo) Stats(context.Context) (*domain.Stats, error) { return nil, nil }

// notifRepo — память-репозиторий напоминаний с дедупликацией по ключу.
type notifRepo struct {
	mu    sync.Mutex
	items map[string]domain.Notification
	list  []domain.Notification
}

func newNotifRepo() *notifRepo { return &notifRepo{items: map[string]domain.Notification{}} }

func (r *notifRepo) Insert(_ context.Context, n *domain.Notification) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := n.TaskID + ":" + n.Type + ":" + n.CreatedAt.Format("2006-01-02")
	if _, ok := r.items[key]; ok {
		return false, nil
	}
	n.ID = int64(len(r.list) + 1)
	r.items[key] = *n
	r.list = append(r.list, *n)
	return true, nil
}

func (r *notifRepo) List(_ context.Context, onlyUnread bool, limit int) ([]domain.Notification, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []domain.Notification{}
	for i := len(r.list) - 1; i >= 0 && len(out) < limit; i-- {
		if onlyUnread && r.list[i].Read {
			continue
		}
		out = append(out, r.list[i])
	}
	return out, nil
}

func (r *notifRepo) MarkRead(_ context.Context, id int64) error { return nil }
func (r *notifRepo) MarkAllRead(_ context.Context) error        { return nil }
func (r *notifRepo) CountUnread(_ context.Context) (int, error) { return 0, nil }

type pub struct {
	mu     sync.Mutex
	events []domain.Event
}

func (p *pub) Publish(_ context.Context, e domain.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, e)
}
func (p *pub) Close() {}

func newNotifier(repo *taskRepo) (*notifier.Notifier, *notifRepo, *pub) {
	np := newNotifRepo()
	p := &pub{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return notifier.New(repo, np, p, metrics.New(), log), np, p
}

func task(id string, due *time.Time, status domain.Status) domain.Task {
	return domain.Task{ID: id, Title: "Задача " + id, DueAt: due, Status: status}
}

// remindersOf отбирает персональные напоминания из всех созданных.
func remindersOf(r *notifRepo) []domain.Notification {
	var out []domain.Notification
	for _, n := range r.list {
		if n.Type == notifier.TypeReminder {
			out = append(out, n)
		}
	}
	return out
}

func TestCheck_ClassificationAndDedupe(t *testing.T) {
	now := time.Date(2026, 10, 25, 15, 0, 0, 0, time.UTC)
	today := time.Date(2026, 10, 25, 18, 0, 0, 0, time.UTC)
	tomorrow := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	overdue := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	far := time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC)

	repo := &taskRepo{tasks: []domain.Task{
		task("today", &today, domain.StatusTodo),
		task("tomorrow", &tomorrow, domain.StatusTodo),
		task("late", &overdue, domain.StatusTodo),
		task("far", &far, domain.StatusTodo),    // вне горизонта
		task("done", &today, domain.StatusDone), // завершённая
		task("nodate", nil, domain.StatusTodo),  // без срока
	}}
	n, np, p := newNotifier(repo)

	created, err := n.Check(context.Background(), now)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if created != 3 {
		t.Fatalf("создано напоминаний = %d, want 3", created)
	}

	byType := map[string]int{}
	for _, n := range np.list {
		byType[n.Type]++
	}
	if byType["due_soon"] != 2 || byType["overdue"] != 1 {
		t.Errorf("типы = %v, want due_soon=2 overdue=1", byType)
	}
	if len(p.events) != 3 {
		t.Errorf("событий Kafka = %d, want 3", len(p.events))
	}

	// Повторная проверка в тот же день ничего не создаёт.
	created2, err := n.Check(context.Background(), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("Check 2: %v", err)
	}
	if created2 != 0 {
		t.Errorf("второй запуск создал %d напоминаний, want 0", created2)
	}

	// На следующий день напоминание о просроченной задаче создаётся снова.
	created3, err := n.Check(context.Background(), now.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("Check 3: %v", err)
	}
	if created3 < 1 {
		t.Error("на следующий день напоминания должны создаться повторно")
	}
}

func TestCheck_OverdueMessage(t *testing.T) {
	now := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	due := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	repo := &taskRepo{tasks: []domain.Task{task("t1", &due, domain.StatusTodo)}}
	n, np, _ := newNotifier(repo)

	if _, err := n.Check(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(np.list) != 1 {
		t.Fatalf("напоминаний = %d, want 1", len(np.list))
	}
	msg := np.list[0].Message
	// 21 → 25 октября: 4 дня просрочки (включая день срока)
	if !strings.Contains(msg, "4 дн") {
		t.Errorf("сообщение = %q, хочу «4 дня/дней»", msg)
	}
}

var _ events.Publisher = (*pub)(nil)

func TestCheck_OnNotifyHook(t *testing.T) {
	now := time.Date(2026, 10, 25, 15, 0, 0, 0, time.UTC)
	today := time.Date(2026, 10, 25, 18, 0, 0, 0, time.UTC)
	overdue := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)

	repo := &taskRepo{tasks: []domain.Task{
		task("today", &today, domain.StatusTodo),
		task("late", &overdue, domain.StatusTodo),
	}}
	n, _, _ := newNotifier(repo)

	var got []domain.Notification
	n.OnNotify = func(x domain.Notification) { got = append(got, x) }
	if _, err := n.Check(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("OnNotify вызван %d раз, want 2", len(got))
	}
	for _, x := range got {
		if x.Title == "" || x.Message == "" || x.TaskID == "" {
			t.Errorf("неполное напоминание: %+v", x)
		}
	}

	// Повторная проверка того же дня не дергает хук (дедуп).
	got = nil
	if _, err := n.Check(context.Background(), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("OnNotify вызван после дедупа %d раз, want 0", len(got))
	}
}

func TestCheck_OnNotifyPanicDoesNotBreakCheck(t *testing.T) {
	now := time.Date(2026, 10, 25, 15, 0, 0, 0, time.UTC)
	due := time.Date(2026, 10, 25, 18, 0, 0, 0, time.UTC)
	repo := &taskRepo{tasks: []domain.Task{task("t", &due, domain.StatusTodo)}}
	n, _, p := newNotifier(repo)
	n.OnNotify = func(domain.Notification) { panic("тост упал") }

	created, err := n.Check(context.Background(), now)
	if err != nil {
		t.Fatalf("Check не должен падать от паники хука: %v", err)
	}
	if created != 1 {
		t.Errorf("created = %d, want 1", created)
	}
	if len(p.events) != 1 {
		t.Errorf("событие Kafka не опубликовано после паники хука: %d", len(p.events))
	}
}

func (r *taskRepo) ListRemindersDue(_ context.Context, now time.Time) ([]domain.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.Task
	for _, t := range r.tasks {
		if t.Status == domain.StatusDone || t.DueAt == nil || t.ReminderOffsetMinutes == nil || t.ReminderSentAt != nil {
			continue
		}
		cutoff := t.DueAt.Add(-time.Duration(*t.ReminderOffsetMinutes) * time.Minute)
		if t.DueAt.After(now) && !cutoff.After(now) {  // граница окна включена, как в SQL (<=)
			out = append(out, t)
		}
	}
	return out, nil
}

func (r *taskRepo) MarkReminderSent(_ context.Context, id string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.tasks {
		if r.tasks[i].ID == id && r.tasks[i].ReminderSentAt == nil {
			ts := at
			r.tasks[i].ReminderSentAt = &ts
		}
	}
	return nil
}

func TestCheck_PersonalReminderOnce(t *testing.T) {
	now := time.Date(2026, 10, 25, 15, 0, 0, 0, time.UTC)
	due := now.Add(10 * time.Minute) // срок через 10 минут
	offset := 15                     // напомнить за 15 минут → уже пора

	t1 := task("rem1", &due, domain.StatusTodo)
	t1.ReminderOffsetMinutes = &offset
	repo := &taskRepo{tasks: []domain.Task{t1}}
	n, np, p := newNotifier(repo)

	created, err := n.Check(context.Background(), now)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	// Задача со сроком через 10 минут попадает и в поток due_soon, и в
	// персональные напоминания: считаем по типам.
	reminders := remindersOf(np)
	if created != 2 {
		t.Fatalf("создано напоминаний = %d (due_soon + reminder), want 2", created)
	}
	if len(reminders) != 1 {
		t.Fatalf("персональных напоминаний = %d, want 1: %+v", len(reminders), np.list)
	}
	if !strings.Contains(reminders[0].Message, "10 мин") {
		t.Errorf("текст = %q, должен упоминать остаток 10 мин", reminders[0].Message)
	}
	// Событие task.reminder в Kafka (рядом с due_soon).
	var reminderEvent bool
	for _, e := range p.events {
		if e.Type == notifier.EventTaskReminder {
			reminderEvent = true
		}
	}
	if !reminderEvent {
		t.Fatalf("нет события task.reminder: %+v", p.events)
	}
	// Отметка отправки зафиксирована.
	if len(repo.tasks) != 1 || repo.tasks[0].ReminderSentAt == nil {
		t.Fatalf("reminder_sent_at не зафиксирован")
	}

	// Повторная проверка до истечения срока — персональное напоминание
	// НЕ дублируется (идемпотентность через reminder_sent_at).
	created, err = n.Check(context.Background(), now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("повторный Check: %v", err)
	}
	if len(remindersOf(np)) != 1 {
		t.Fatalf("персональное напоминание задублировалось: %+v", np.list)
	}

	// Смена срока сбрасывает отметку: напоминание придёт по новому сроку.
	newDue := now.Add(2 * time.Hour)
	repo.tasks[0].DueAt = &newDue
	repo.tasks[0].ReminderSentAt = nil
	created, err = n.Check(context.Background(), now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("Check после смены срока: %v", err)
	}
	if created != 0 {
		t.Errorf("до наступления окна напоминания напоминать рано: created=%d", created)
	}
}

func TestCheck_ReminderWindowBoundary(t *testing.T) {
	now := time.Date(2026, 10, 25, 15, 0, 0, 0, time.UTC)
	offset := 15

	// Ровно на границе окна (осталось ровно offset минут) — напомнить пора.
	due := now.Add(15 * time.Minute)
	t1 := task("edge", &due, domain.StatusTodo)
	t1.ReminderOffsetMinutes = &offset
	n, np, _ := newNotifier(&taskRepo{tasks: []domain.Task{t1}})
	if _, err := n.Check(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(remindersOf(np)) != 1 {
		t.Fatalf("на границе окна персональное напоминание должно создаться: %+v", np.list)
	}

	// Слишком рано (осталось больше offset) — не напоминать.
	due2 := now.Add(16 * time.Minute)
	t2 := task("early", &due2, domain.StatusTodo)
	t2.ReminderOffsetMinutes = &offset
	n2, np2, _ := newNotifier(&taskRepo{tasks: []domain.Task{t2}})
	if _, err := n2.Check(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(remindersOf(np2)) != 0 {
		t.Fatalf("раннее персональное напоминание: %+v", np2.list)
	}

	// Срок уже наступил — персональное напоминание не нужно (поток overdue).
	due3 := now.Add(-5 * time.Minute)
	t3 := task("past", &due3, domain.StatusTodo)
	t3.ReminderOffsetMinutes = &offset
	n3, np3, _ := newNotifier(&taskRepo{tasks: []domain.Task{t3}})
	if _, err := n3.Check(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	for _, nt := range np3.list {
		if nt.Type == notifier.TypeReminder {
			t.Fatalf("для наступившего срока персональное напоминание не должно приходить: %+v", np3.list)
		}
	}
}
