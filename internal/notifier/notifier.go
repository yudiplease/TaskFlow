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
	TypeDueSoon = "due_soon"
	TypeOverdue = "overdue"
)

// События Kafka, публикуемые нотификатором.
const (
	EventTaskDueSoon = "task.due_soon"
	EventTaskOverdue = "task.overdue"
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
		if t.DueDate == nil {
			continue
		}
		var nType, message, eventType string
		switch {
		case t.DueDate.Before(todayStart):
			days := int(todayStart.Sub(*t.DueDate).Hours()/24) + 1
			if days < 1 {
				days = 1
			}
			nType = TypeOverdue
			message = fmt.Sprintf("Просрочена на %d %s (срок: %s)", days, pluralDays(days), t.DueDate.Format("02.01"))
			eventType = EventTaskOverdue
		case t.DueDate.Before(todayStart.Add(24 * time.Hour)):
			nType = TypeDueSoon
			message = "Срок сегодня — самое время заняться"
			eventType = EventTaskDueSoon
		default:
			nType = TypeDueSoon
			message = "Срок завтра — можно спланировать заранее"
			eventType = EventTaskDueSoon
		}

		notification := &domain.Notification{
			TaskID:    t.ID,
			Type:      nType,
			Title:     t.Title,
			Message:   message,
			CreatedAt: now,
		}
		wasCreated, err := n.nots.Insert(ctx, notification)
		if err != nil {
			n.log.Error("создание напоминания", "task", t.ID, "err", err)
			continue
		}
		if !wasCreated {
			continue // уже напомнили сегодня
		}
		created++
		n.m.NotificationsCreated.WithLabelValues(nType).Inc()

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

		n.pub.Publish(ctx, domain.Event{
			Type:      eventType,
			TaskID:    t.ID,
			Actor:     "планировщик",
			Timestamp: now,
			Payload: map[string]any{
				"title":    t.Title,
				"due_date": t.DueDate.Format(time.RFC3339),
			},
		})
	}
	if created > 0 {
		n.log.Info("созданы напоминания", "count", created)
	}
	return created, nil
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
