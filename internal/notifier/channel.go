package notifier

import (
	"context"
	"log/slog"

	"tasktracker/internal/domain"
)

// Channel — внешний канал доставки напоминаний (email, push, SMS, …).
// In-app-канал — таблица notifications (колокольчик в UI) — реализован
// напрямую через NotificationRepository и работает всегда; дополнительные
// каналы подключаются реализацией интерфейса и передачей в Notifier.Channels.
type Channel interface {
	Name() string
	Send(ctx context.Context, n domain.Notification) error
}

// LogChannel — заглушка канала: пишет напоминание в лог. Точка расширения
// для будущих email/push-интеграций: скопировать, заменить тело Send
// реальной отправкой и добавить экземпляр в Notifier.Channels.
type LogChannel struct {
	Log *slog.Logger
}

func (c LogChannel) Name() string { return "log" }

func (c LogChannel) Send(ctx context.Context, n domain.Notification) error {
	if c.Log != nil {
		c.Log.Info("напоминание (заглушка канала)", "type", n.Type, "task", n.TaskID, "message", n.Message)
	}
	return nil
}
