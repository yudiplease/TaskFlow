package service

import (
	"context"
	"sync"
	"time"

	"tasktracker/internal/domain"
	"tasktracker/internal/storage"
)

// DefaultsService — «залипающие» значения формы новой задачи (per-user).
// Сервер — источник истины: клиент после каждого сохранения задачи
// кладёт сюда свежие значения, при загрузке — сверяет updated_at.
type DefaultsService struct {
	repo storage.DefaultsRepository
}

// NewDefaultsService собирает сервис.
func NewDefaultsService(repo storage.DefaultsRepository) *DefaultsService {
	return &DefaultsService{repo}
}

// Get возвращает значения пользователя; если их ещё нет — пустые
// (форма возьмёт базовые дефолты). Историческая запись с невалидными
// значениями не ломает форму: она отбрасывается.
func (s *DefaultsService) Get(ctx context.Context, actor string) (domain.TaskDefaults, time.Time, error) {
	d, ts, err := s.repo.Get(ctx, actor)
	if err != nil {
		if IsNotFound(err) {
			return domain.TaskDefaults{}, time.Time{}, nil
		}
		return domain.TaskDefaults{}, time.Time{}, err
	}
	norm, err := d.Normalize()
	if err != nil {
		return domain.TaskDefaults{}, time.Time{}, nil
	}
	return norm, ts, nil
}

// Put валидирует и сохраняет значения, возвращая каноническую форму и время записи.
func (s *DefaultsService) Put(ctx context.Context, actor string, d domain.TaskDefaults) (domain.TaskDefaults, time.Time, error) {
	norm, err := d.Normalize()
	if err != nil {
		return domain.TaskDefaults{}, time.Time{}, err
	}
	ts, err := s.repo.Put(ctx, actor, norm)
	if err != nil {
		return domain.TaskDefaults{}, time.Time{}, err
	}
	return norm, ts, nil
}

// StickyPatch — изменение sticky-полей из запроса сохранения задачи.
// Ненулевой указатель = поле реально присутствовало в запросе и его
// значение нужно применить (пустая строка/срез = сбросить запомненное);
// nil = поле в запросе отсутствовало и запомненное трогать нельзя.
type StickyPatch struct {
	Status   *string
	Priority *string
	Color    *string
	Assignee *string
	Labels   *[]string
}

// Empty сообщает, есть ли вообще что обновлять.
func (p StickyPatch) Empty() bool {
	return p.Status == nil && p.Priority == nil && p.Color == nil && p.Assignee == nil && p.Labels == nil
}

// mergeMu сериализует read-modify-write в ApplyPatch внутри процесса:
// без него два параллельных сохранения задач перезаписали бы поля друг друга.
var mergeMu sync.Mutex

// ApplyPatch накладывает патч на запомненные значения пользователя и
// сохраняет результат. Вызывается после успешного сохранения задачи:
// ошибка здесь не должна влиять на уже выполненное сохранение.
func (s *DefaultsService) ApplyPatch(ctx context.Context, actor string, p StickyPatch) (domain.TaskDefaults, time.Time, error) {
	if p.Empty() {
		return s.Get(ctx, actor)
	}

	mergeMu.Lock()
	defer mergeMu.Unlock()

	current, _, err := s.repo.Get(ctx, actor)
	if err != nil {
		if !IsNotFound(err) {
			return domain.TaskDefaults{}, time.Time{}, err
		}
		current = domain.TaskDefaults{}
	}

	merged := current
	if p.Status != nil {
		merged.Status = *p.Status
	}
	if p.Priority != nil {
		merged.Priority = *p.Priority
	}
	if p.Color != nil {
		merged.Color = *p.Color
	}
	if p.Assignee != nil {
		merged.Assignee = *p.Assignee
	}
	if p.Labels != nil {
		merged.Labels = *p.Labels
	}

	norm, err := merged.Normalize()
	if err != nil {
		// Сохранённые значения стали невалидны (ручная правка БД и т.п.) —
		// начинаем с чистого листа, оставляя только сам патч: он уже
		// прошёл валидацию сервиса задач.
		fresh := domain.TaskDefaults{}
		if p.Status != nil {
			fresh.Status = *p.Status
		}
		if p.Priority != nil {
			fresh.Priority = *p.Priority
		}
		if p.Color != nil {
			fresh.Color = *p.Color
		}
		if p.Assignee != nil {
			fresh.Assignee = *p.Assignee
		}
		if p.Labels != nil {
			fresh.Labels = *p.Labels
		}
		if norm, err = fresh.Normalize(); err != nil {
			return domain.TaskDefaults{}, time.Time{}, err
		}
	}

	ts, err := s.repo.Put(ctx, actor, norm)
	if err != nil {
		return domain.TaskDefaults{}, time.Time{}, err
	}
	return norm, ts, nil
}
