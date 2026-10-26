// Package notifier — фоновый планировщик напоминаний о сроках задач.
//
// Раз в минуту проверяет незавершённые задачи со сроком «сегодня»,
// «завтра» и просроченные; создаёт напоминания (не чаще одного
// на задачу/тип/день) и публикует события в Kafka.
package notifier

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"tasktracker/internal/domain"
	"tasktracker/internal/events"
	"tasktracker/internal/metrics"
	"tasktracker/internal/storage"
)

// Типы напоминаний.
const (
	TypeDueSoon  = "due_soon"
	TypeOverdue  = "overdue"
	TypeReminder = "reminder" // персональное: «напомнить за N минут до срока»
)

// События Kafka, публикуемые нотификатором.
const (
	EventTaskDueSoon  = "task.due_soon"
	EventTaskOverdue  = "task.overdue"
	EventTaskReminder = "task.reminder"
)

// Notifier создаёт напоминания по расписанию.
type Notifier struct {
	tasks storage.TaskRepository
	nots  storage.NotificationRepository
	pub   events.Publisher
	m     *metrics.Metrics
	log   *slog.Logger
	// OnNotify вызывается синхронно после КАЖДОГО созданного напоминания.
	// Используется десктопом для системных тостов; nil = ничего не делать.
	OnNotify func(n domain.Notification)
	// Channels — дополнительные каналы доставки (email, push, …); см. Channel.
	// nil/пустой = только in-app (колокольчик).
	Channels []Channel
}

// New собирает нотификатор.
func New(tasks storage.TaskRepository, nots storage.NotificationRepository, pub events.Publisher, m *metrics.Metrics, log *slog.Logger) *Notifier {
	return &Notifier{tasks: tasks, nots: nots, pub: pub, m: m, log: log}
}

// checkTimeout — бюджет одной проверки сроков: зависший запрос к БД
// не блокирует планировщик навсегда.
const checkTimeout = 30 * time.Second

// Run блокирует горутину, запуская Check по тикеру. Пании внутри Check
// перехватываются (планировщик продолжает работу), ошибки логируются.
func (n *Notifier) Run(ctx context.Context, every time.Duration) {
	// Первая проверка — сразу после старта (с небольшой паузой на прогрев).
	select {
	case <-time.After(5 * time.Second):
	case <-ctx.Done():
		return
	}
	n.safeCheck(ctx)

	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			n.safeCheck(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// safeCheck выполняет Check с таймаутом и защитой от паник.
func (n *Notifier) safeCheck(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			n.log.Error("паника при проверке сроков", "panic", r)
		}
	}()

	checkCtx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	if _, err := n.Check(checkCtx, time.Now().UTC()); err != nil && ctx.Err() == nil {
		n.log.Warn("проверка сроков", "err", err)
	}
}

// Check создаёт напоминания на момент now и возвращает их число.
func (n *Notifier) Check(ctx context.Context, now time.Time) (int, error) {
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	horizon := todayStart.Add(48 * time.Hour) // сегодня + завтра

	due, err := n.tasks.ListDue(ctx, horizon)
	if err != nil {
		return 0, err
	}

	created := 0
	for _, t := range due {
		if t.DueAt == nil {
			continue
		}
		var nType, message, eventType string
		switch {
		case t.DueAt.Before(todayStart):
			days := int(todayStart.Sub(*t.DueAt).Hours()/24) + 1
			if days < 1 {
				days = 1
			}
			nType = TypeOverdue
			message = fmt.Sprintf("Просрочена на %d %s (срок: %s)", days, pluralDays(days), t.DueAt.Format("02.01 15:04"))
			eventType = EventTaskOverdue
		case t.DueAt.Before(todayStart.Add(24 * time.Hour)):
			nType = TypeDueSoon
			message = "Срок сегодня — самое время заняться"
			eventType = EventTaskDueSoon
		default:
			nType = TypeDueSoon
			message = "Срок завтра — можно спланировать заранее"
			eventType = EventTaskDueSoon
		}

		wasCreated, err := n.notify(ctx, now, t, nType, message)
		if err != nil {
			n.log.Error("создание напоминания", "task", t.ID, "err", err)
			continue
		}
		if !wasCreated {
			continue // уже напомнили сегодня
		}
		created++
		n.m.NotificationsCreated.WithLabelValues(nType).Inc()

		n.pub.Publish(ctx, domain.Event{
			Type:      eventType,
			TaskID:    t.ID,
			Actor:     "планировщик",
			Timestamp: now,
			Payload: map[string]any{
				"title":    t.Title,
				"due_at":   t.DueAt.Format(time.RFC3339),
				"all_day":  t.DueAllDay,
			},
		})
	}

	// Персональные напоминания «за N минут»: срок ещё не наступил,
	// до него осталось не больше reminder_offset_minutes.
	reminders, err := n.tasks.ListRemindersDue(ctx, now)
	if err != nil {
		return created, err
	}
	for _, t := range reminders {
		if t.DueAt == nil || t.ReminderOffsetMinutes == nil {
			continue
		}
		left := t.DueAt.Sub(now)
		if left < 0 {
			left = 0
		}
		message := fmt.Sprintf("Напоминание: срок через %s (%s)",
			pluralMinutes(int(left.Minutes())), t.DueAt.Local().Format("15:04"))
		wasCreated, err := n.notify(ctx, now, t, TypeReminder, message)
		if err != nil {
			n.log.Error("создание напоминания-напоминалки", "task", t.ID, "err", err)
			continue
		}
		if !wasCreated {
			continue
		}
		created++
		n.m.NotificationsCreated.WithLabelValues(TypeReminder).Inc()

		// Идемпотентность: фиксируем отправку, чтобы напоминание пришло один раз.
		if err := n.tasks.MarkReminderSent(ctx, t.ID, now); err != nil {
			n.log.Error("фиксация отправки напоминания", "task", t.ID, "err", err)
		}

		n.pub.Publish(ctx, domain.Event{
			Type:      EventTaskReminder,
			TaskID:    t.ID,
			Actor:     "планировщик",
			Timestamp: now,
			Payload: map[string]any{
				"title": t.Title,
				"due_at": t.DueAt.Format(time.RFC3339),
				"offset_minutes": *t.ReminderOffsetMinutes,
			},
		})
	}

	if created > 0 {
		n.log.Info("созданы напоминания", "count", created)
	}
	return created, nil
}

// notify пишет in-app-напоминание (дедупликация внутри репозитория),
// рассылает по внешним каналам и дергает хук OnNotify.
func (n *Notifier) notify(ctx context.Context, now time.Time, t domain.Task, nType, message string) (bool, error) {
	notification := &domain.Notification{
		TaskID:    t.ID,
		Type:      nType,
		Title:     t.Title,
		Message:   message,
		CreatedAt: now,
	}
	wasCreated, err := n.nots.Insert(ctx, notification)
	if err != nil || !wasCreated {
		return wasCreated, err
	}

	for _, ch := range n.Channels {
		if err := ch.Send(ctx, *notification); err != nil {
			n.log.Warn("канал напоминаний", "channel", ch.Name(), "task", t.ID, "err", err)
		}
	}

	if n.OnNotify != nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					n.log.Error("паника в обработчике напоминания", "panic", r)
				}
			}()
			n.OnNotify(*notification)
		}()
	}
	return true, nil
}

// pluralDays склоняет слово «день» по числу: 1 день, 2 дня, 5 дней.
func pluralDays(n int) string {
	switch {
	case n%10 == 1 && n%100 != 11:
		return "день"
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 10 || n%100 >= 20):
		return "дня"
	default:
		return "дней"
	}
}

// pluralMinutes форматирует остаток до срока: «3 мин», «1 ч 5 мин».
func pluralMinutes(total int) string {
	if total < 60 {
		if total < 1 {
			total = 1
		}
		return fmt.Sprintf("%d мин", total)
	}
	h := total / 60
	m := total % 60
	if m == 0 {
		if h == 1 {
			return "1 час"
		}
		return fmt.Sprintf("%d ч", h)
	}
	return fmt.Sprintf("%d ч %d мин", h, m)
}
