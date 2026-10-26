# TaskFlow — трекер задач на Go · Kafka · PostgreSQL

## 🚀 Просто хочу приложение (Windows)

> [**⬇️ Скачать установщик TaskFlow**](https://github.com/yudiplease/taskflow/releases/latest/download/TaskFlow-Setup.exe)

Скачайте и запустите — **сервер, Docker, PostgreSQL и Kafka не нужны**: это обычное десктоп-приложение, все данные хранятся локально в SQLite. Ссылка всегда ведёт на актуальную версию.

<details>
<summary>Что умеет десктоп-версия</summary>

- Канбан-доска с drag & drop, календарь с планированием дат
- Напоминания о сроках + **системные уведомления Windows**
- Установка без прав администратора, без окна консоли
- Один exe (~16 МБ), чистый Go без CGO

</details>

---

Канбан-трекер с событийным конвейером и полным циклом наблюдаемости. Один код — два облика:

- **Сервер** (`docker compose up`): полный стек — Go API, PostgreSQL, Kafka, Prometheus, Grafana.
- **Десктоп** (`bin/TaskFlow.exe`): одно приложение без сети — то же ядро, встроенный SQLite и локальная событийная шина, нативное окно WebView2.

```
Браузер ──HTTP──▶ Go API ──SQL──▶ PostgreSQL (tasks)
                    │
                    ├──▶ Kafka (task-events) ──▶ Консьюмер ──▶ PostgreSQL (activity)
                    │
                    └──▶ /metrics ──▶ Prometheus ──▶ Grafana

Десктоп:  Окно WebView2 ──▶ Go API ──SQL──▶ SQLite (файл рядом с exe)
                             └──▶ LocalBus (события в журнал синхронно)
```

## Возможности

- **Канбан-доска**: 5 колонок, drag & drop с сохранением позиции, тёмная тема, поиск, фильтры, живая статистика.
- **Цветовые метки**: фиксированный справочник из 8 цветов (🔴🟠🟡🟢🔵🟣⚪⚫) — палитра в форме, кружок на карточке, фильтр по цвету и легенда; справочник отдаёт `GET /api/colors`.
- **Значения по умолчанию («залипающие» параметры)**: форма новой задачи предзаполняется значениями из последней созданной/отредактированной задачи (колонка, приоритет, цвет, исполнитель, метки). Значения хранятся на сервере per-user (SQLite/PostgreSQL) со сверкой по `updated_at` против локального кэша; в настройках — выключатель запоминания и сброс полей; предзаполнение можно отменить кнопкой «Сбросить» прямо в форме.
- **Календарь**: месячная сетка с задачами по датам, перенос задачи на другой день перетаскиванием, планирование несроковых задач (перетащите чип из «Без срока» в день), создание задачи на конкретный день.
- **Срок с точностью до минуты**: `due_at` (timestamptz, UTC) + `due_all_day` для «весь дня» (инстант = 23:59 локального дня) + `due_timezone` (IANA-зона постановки). DateTimePicker с пресетами («Сегодня 18:00», «Завтра 10:00», «Пн 09:00», «+1 час»), чекбокс «Весь день», отображение через `Intl.DateTimeFormat`; индикация близости: просрочено — красный, <24 ч — оранжевый, <1 ч — акцент с пульсацией (отключается `prefers-reduced-motion`).
- **Уведомления**: фоновый планировщик раз в минуту напоминает о задачах со сроком «сегодня»/«завтра» и о просроченных (не чаще одного напоминания на задачу/тип/день), плюс персональные напоминания «за N минут» до срока (`reminder_offset_minutes`, идемпотентность через `reminder_sent_at`; каналы — интерфейс `notifier.Channel`, in-app работает всегда). Колокольчик с бейджем, панель напоминаний, тосты; браузерные push (веб) или системные тосты Windows (десктоп, через Shell_NotifyIcon).
- **Ассистентка Ася** (десктоп): анимированная чиби-помощница — реагирует на события доски, празднует завершённые задачи, напоминает о просрочках; озвучка реплик через системный синтез речи (RHVoice), управление кнопками 🗣️/🔊.
- **События через Kafka**: каждое действие (включая напоминания планировщика) публикуется в топик `task-events`; консьюмер пишет журнал активности в PostgreSQL. Ключ партиции = ID задачи → события одной задачи упорядочены.
- **Устойчивость**: при недоступном Kafka API продолжает работать (буфер + повторы); PostgreSQL обязателен и проверяется readiness-пробой.
- **Конкурентность**: вся фоновая работа — горутины под supervision (перехват паник, перезапуск с backoff, лимит рестартов для crash-loop), неблокирующая публикация событий, таймауты на все внешние операции, graceful shutdown с дедлайном на каждый шаг. Тесты прогоняются с `-race` (см. ниже).
- **Мониторинг**: Prometheus-метрики (HTTP RPS/латентность, операции над задачами, события Kafka, напоминания, рантайм Go) + готовый дашборд Grafana.
- **Тесты**: unit-тесты сервиса, обработчиков и планировщика напоминаний + интеграционные тесты хранилища за build-тегом `integration`.

## Быстрый старт (Docker)

```bash
docker compose up --build
```

| Сервис | Адрес | Описание |
|---|---|---|
| Веб-интерфейс + API | http://localhost:8080 | канбан-доска (демо-данные засеются сами) |
| Grafana | http://localhost:3000 | дашборд «TaskFlow — трекер задач» (login: admin/admin) |
| Prometheus | http://localhost:9090 | сырые метрики |
| PostgreSQL | localhost:5433 | tasktracker/tasktracker |
| Kafka (host) | localhost:29092 | снаружи compose-сети |

Остановка: `docker compose down` (данные в volume сохранятся; `docker compose down -v` — удалить).

## Десктоп-приложение (Windows)

Быстрая сборка exe с иконкой, метаданными версии и установщиком одним скриптом:

```bat
build\make-installer.bat
```

Результат: `bin\TaskFlow.exe` (16 МБ, иконка и VersionInfo встроены через `goversioninfo`) и `dist\TaskFlow-Setup-<версия>.exe` — установщик Inno Setup.

Если нужен только exe без установщика:

```bash
go build -trimpath -ldflags="-s -w" -o bin/TaskFlow.exe ./cmd/desktop
bin\TaskFlow.exe
```

Свойства установщика:
- пер-юзерная установка без прав администратора: `%LOCALAPPDATA%\Programs\TaskFlow`;
- ярлыки: меню «Пуск» всегда, рабочий стол — галочкой, автозапуск — опционально;
- русский и английский интерфейс мастера, корректный uninstall (данные пользователя сохраняются);
- смена иконки/версии: правьте `build/icon.png` → перегенерируйте `icon.ico` (Pillow, см. историю проекта) и `build/versioninfo.json`.

Свойства приложения:
- Один exe, чистый Go без CGO: домен, сервис, API и интерфейс — те же, что на сервере.
- Без окна консоли (subsystem GUI); логи — `data/taskflow.log`.
- Демо-задачи не создаются: десктоп стартует с чистой доской (демо-режим только у docker-версии).
- Окно масштабируется под рабочий стол пользователя (~86%×90% рабочей области), манифест PerMonitorV2 — чёткий рендер на экранах с масштабированием.
- Данные — `data/taskflow.db` (SQLite, WAL) рядом с exe; если каталог read-only, база уходит в `%LOCALAPPDATA%\TaskFlow`.
- Kafka и PostgreSQL не нужны: события доставляет локальная шина `events.LocalBus`, семантика журнала активности и метрик сохранена.
- Окно — системный WebView2 (предустановлен в Windows 10/11); при недоступном рантайме приложение само откроет браузер.
- HTTP слушает только 127.0.0.1 на случайном свободном порту; закрытие окна = корректный выход (graceful shutdown).
- Флаги: `--headless` (без окна, для отладки), `--db ПУТЬ`, `--port N`.

## Локальный запуск без Docker (нужен только PostgreSQL)

```bash
# Kafka необязательна:
KAFKA_ENABLED=false \
POSTGRES_DSN='postgres://tasktracker:tasktracker@localhost:5432/tasktracker?sslmode=disable' \
go run ./cmd/server
```

## API

| Метод | Путь | Описание |
|---|---|---|
| GET | `/api/tasks?status=&priority=&assignee=&label=&q=&limit=&offset=` | список с фильтрами |
| POST | `/api/tasks` | создать `{title, description, status, priority, assignee, labels[], due_at, due_all_day, due_timezone, reminder_offset_minutes}`; устаревший `due_date` тоже принимается (трактуется как «весь день») |
| GET | `/api/tasks/{id}` | задача |
| PATCH | `/api/tasks/{id}` | частичное обновление; пустой `due_at` сбрасывает срок, `reminder_offset_minutes: null` убирает напоминание |
| DELETE | `/api/tasks/{id}` | удалить |
| POST | `/api/tasks/{id}/move` | перенос `{status, position}` |
| GET | `/api/tasks/{id}/activity` | история задачи |
| GET | `/api/activity?limit=` | общий журнал (Kafka → Postgres) |
| GET | `/api/notifications?limit=` | напоминания + число непрочитанных |
| POST | `/api/notifications/{id}/read` | отметить прочитанным |
| POST | `/api/notifications/read-all` | прочитать все |
| GET | `/api/stats` | сводка для дашборда |
| GET | `/healthz` · `/readyz` | пробы живости/готовности |
| GET | `/metrics` | метрики Prometheus |

Заголовок `X-Actor` задаёт автора действия (по умолчанию `system`).

## Тесты

```bash
go test ./...                                  # unit
go test -tags integration ./internal/storage/postgres   # интеграционные (нужен PostgreSQL)
```

С race-детектором (нужен CGO; на Windows без gcc — в контейнере):

```bash
docker run --rm -v "%cd%:/src" -w //src golang:1.27 go test -race ./...
```

## Модель конкурентности

| Компонент | Механизм защиты |
|---|---|
| Публикация в Kafka | Неблокирующая очередь (`Publish` никогда не ждёт брокера); воркер-горутина с FIFO-доставкой, 3 попытки с backoff; `Publish` после `Close` безопасен — событие просто отбрасывается, паника `send on closed channel` исключена (канал не закрывается никогда) |
| Drain при shutdown | `Close` даёт воркеру ограниченное время досыпать буфер (одна попытка на событие), затем завершается принудительно |
| Консьюмер Kafka | recover вокруг обработки (паника не роняет процесс), таймаут 10 с на вставку в PostgreSQL; офсет коммитится только после успеха → зависшая вставка не теряет событие, доставка повторится |
| Планировщик напоминаний | Таймаут 30 с на проверку + recover от паник |
| Supervision (`internal/supervisor`) | Каждая фоновая горутина под контролем: паника → лог + перезапуск с экспоненциальным backoff (200 мс → 5 с), максимум 5 рестартов подряд для crash-loop, счётчик сбрасывается после 3 с стабильной работы |
| Shutdown | Строго ограничен по времени: HTTP `Shutdown` с дедлайном → `supervisor.Wait` с дедлайном → закрытие продюсера с дедлайном → закрытие пула БД. Ни один шаг не висит вечно |
| HTTP | Таймауты Read/Write/Idle; recover-middleware превращает панику обработчика в 500; метрики потокобезопасны (prometheus client) |

## Конфигурация (переменные окружения)

| Переменная | По умолчанию | Описание |
|---|---|---|
| `PORT` | `8080` | порт HTTP |
| `POSTGRES_DSN` | `postgres://tasktracker:tasktracker@localhost:5432/tasktracker?sslmode=disable` | подключение к БД |
| `KAFKA_BROKERS` | `localhost:29092` | брокеры через запятую |
| `KAFKA_TOPIC` | `task-events` | топик событий |
| `KAFKA_GROUP` | `activity-writer` | группа консьюмера |
| `KAFKA_ENABLED` | `true` | `false` — работать без Kafka |

## Структура

```
cmd/server/            точка входа сервера (API + консьюмер + планировщик)
cmd/desktop/           точка входа десктоп-приложения (SQLite + окно WebView2)
internal/domain/       модели: Task, Status, Priority, Event, Notification, Stats
internal/config/       конфигурация из окружения
internal/storage/      интерфейсы репозиториев
internal/storage/postgres/  pgx-реализация + встроенные миграции (сервер)
internal/storage/sqlite/    modernc/sqlite-реализация + миграции (десктоп)
internal/events/       Kafka-продюсер/консьюмер + локальная шина LocalBus
internal/notifier/     планировщик напоминаний о сроках
internal/service/      бизнес-логика, валидация, публикация событий
internal/httpapi/      REST-обработчики, middleware, static
internal/supervisor/   контроль фоновых горутин: recover, restart, bounded shutdown
internal/metrics/      метрики Prometheus
web/                   канбан + календарь + уведомления (vanilla JS, встраивается в бинарь)
monitoring/            prometheus.yml + provisioning Grafana + дашборд
```
