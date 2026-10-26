package service

import (
	"context"

	"tasktracker/internal/domain"
	"tasktracker/internal/storage"
)

// Notifications — сценарии работы с напоминаниями.
type Notifications struct {
	repo storage.NotificationRepository
}

// NewNotifications собирает сервис напоминаний.
func NewNotifications(repo storage.NotificationRepository) *Notifications {
	return &Notifications{repo: repo}
}

// ListResult — ответ списка напоминаний.
type ListResult struct {
	Notifications []domain.Notification `json:"notifications"`
	Unread        int                   `json:"unread"`
}

// List возвращает последние напоминания и число непрочитанных.
func (s *Notifications) List(ctx context.Context, limit int) (ListResult, error) {
	items, err := s.repo.List(ctx, false, limit)
	if err != nil {
		return ListResult{}, err
	}
	unread, err := s.repo.CountUnread(ctx)
	if err != nil {
		return ListResult{}, err
	}
	if items == nil {
		items = []domain.Notification{}
	}
	return ListResult{Notifications: items, Unread: unread}, nil
}

// MarkRead отмечает напоминание прочитанным.
func (s *Notifications) MarkRead(ctx context.Context, id int64) error {
	return s.repo.MarkRead(ctx, id)
}

// MarkAllRead отмечает все напоминания прочитанными.
func (s *Notifications) MarkAllRead(ctx context.Context) error {
	return s.repo.MarkAllRead(ctx)
}
