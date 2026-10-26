//go:build windows

// TaskFlow Desktop — десктопная версия трекера: одно приложение,
// локальная база SQLite, окно WebView2. Сетевые зависимости не нужны.
//
// Архитектурно это то же ядро (домен, сервис, REST API, веб-интерфейс):
// PostgreSQL заменён встроенным SQLite, Kafka — синхронной событийной
// шиной internal/events.LocalBus (журнал активности и метрики работают).
//
// Флаги:
//
//	--headless  не открывать окно (для отладки/тестов)
//	--db PATH   путь к файлу базы (по умолчанию data/taskflow.db рядом с exe)
//	--port N    фиксированный порт HTTP (по умолчанию свободный случайный)
package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"
	// База часовых поясов встраивается в exe: на Windows без Go нет системной
	// tzdata, а LoadLocation нужен для валидации due_timezone и sticky-времени.
	_ "time/tzdata"

	"github.com/jchv/go-webview2"

	"tasktracker/internal/config"
	"tasktracker/internal/domain"
	"tasktracker/internal/events"
	"tasktracker/internal/httpapi"
	"tasktracker/internal/metrics"
	"tasktracker/internal/notifier"
	"tasktracker/internal/service"
	"tasktracker/internal/storage/sqlite"
	"tasktracker/internal/supervisor"
	"tasktracker/internal/winnotify"
)

func main() {
	headless := flag.Bool("headless", false, "запустить без окна (HTTP+API только в консоли)")
	dbPath := flag.String("db", "", "путь к файлу базы SQLite")
	port := flag.Int("port", 0, "порт HTTP (0 — случайный свободный)")
	flag.Parse()

	if *dbPath == "" {
		*dbPath = defaultDBPath(nil)
	}

	// Логи дублируются в файл рядом с базой: exe собран с -H windowsgui
	// и консоли не имеет — файл остаётся единственным следом работы.
	log := newLogger(filepath.Dir(*dbPath))
	slog.SetDefault(log)
	if err := os.MkdirAll(filepath.Dir(*dbPath), 0o755); err != nil {
		log.Error("не удалось создать каталог данных", "dir", filepath.Dir(*dbPath), "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := sqlite.Open(ctx, *dbPath, log)
	if err != nil {
		log.Error("открытие локальной базы", "path", *dbPath, "err", err)
		os.Exit(1)
	}
	log.Info("локальная база готова", "path", *dbPath)

	m := metrics.New()
	tasksRepo := sqlite.NewTasks(store)
	activityRepo := sqlite.NewActivity(store)
	notifRepo := sqlite.NewNotifications(store)

	// Событийная шина: та же семантика «событие → журнал активности»,
	// что и через Kafka на сервере, но без сети.
	bus := events.NewLocalBus(log, func(ctx context.Context, e domain.Event) {
		insertCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		act := &domain.Activity{
			TaskID:    e.TaskID,
			Type:      e.Type,
			Actor:     e.Actor,
			Payload:   e.Payload,
			CreatedAt: eventTime(e),
		}
		if err := activityRepo.Insert(insertCtx, act); err != nil {
			log.Error("запись активности (локальная шина)", "err", err, "type", e.Type)
			return
		}
		m.EventsConsumedTotal.WithLabelValues(e.Type).Inc()
	})

	svc := service.NewTaskService(tasksRepo, activityRepo, bus, m)
	notificationsSvc := service.NewNotifications(notifRepo)
	defaultsSvc := service.NewDefaultsService(sqlite.NewDefaults(store))

	workers := supervisor.NewGroup(ctx, log, supervisor.Options{})
	notif := notifier.New(tasksRepo, notifRepo, bus, m, log)
	// Системные тосты Windows при создании напоминания; HWND окна
	// появляется чуть позже — читаем атомарно на каждый вызов.
	var hwnd atomic.Uintptr
	notif.OnNotify = func(n domain.Notification) {
		h := hwnd.Load()
		if h == 0 {
			log.Warn("окно ещё не создано — системный тост пропущен", "title", n.Title)
			return
		}
		if err := winnotify.Show(h, n.Title, n.Message); err != nil {
			log.Warn("системное уведомление", "err", err, "title", n.Title)
		}
	}
	workers.Go("notifier", func(wctx context.Context) { notif.Run(wctx, time.Minute) })

	// HTTP на 127.0.0.1 — интерфейс доступен только локально.
	listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(*port))
	if err != nil {
		log.Error("не удалось занять порт", "err", err)
		os.Exit(1)
	}
	addr := listener.Addr().String()

	api := httpapi.NewServer(httpapi.Deps{
		Config:        config.Load(),
		Service:       svc,
		Notifications: notificationsSvc,
		Defaults:      defaultsSvc,
		Metrics:       m,
		Log:           log,
		PingPostgres: func() error {
			c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			return store.Ping(c)
		},
		KafkaHealthy: func() bool { return true },
		Mode:         "desktop",
	})

	handler := api.Handler()
	// Озвучка Аси: POST /api/tts синтезирует WAV через SAPI (RHVoice) —
	// безопасно в любом режиме, включая headless.
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/tts", ttsHandler(filepath.Dir(*dbPath), log))
	mux.Handle("/", handler)
	handler = mux

	srv := &http.Server{
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
	}
	serverDone := make(chan error, 1)
	go func() {
		log.Info("локальный сервер запущен", "url", "http://"+addr+"/")
		serverDone <- srv.Serve(listener)
	}()

	url := "http://" + addr + "/"

	// Падение сервера должно закрывать и окно.
	go func() {
		if err := <-serverDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("локальный сервер упал", "err", err)
			stop()
		}
	}()

	if *headless {
		log.Info("headless-режим: окно не открывается", "url", url)
		<-ctx.Done()
		log.Info("получен сигнал завершения")
	} else {
		// Окно WebView2 и его цикл сообщений обязаны жить на главном
		// потоке процесса: инициализация WebView2 использует COM, и при
		// создании из случайной горутины она периодически зависает.
		runtime.LockOSThread()
		openWindow(ctx, url, filepath.Dir(*dbPath), &hwnd, log)
		log.Info("окно приложения закрыто")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	workers.Wait(8 * time.Second)
	_ = store.Close()
	log.Info("приложение остановлено")
}

// openWindow открывает нативное окно WebView2 и блокирует главный поток
// до закрытия окна; при отмене контекста окно закрывается принудительно.
// При недоступном рантайме (редкие «голые» Windows) открывает браузер.
func openWindow(ctx context.Context, url, dataPath string, hwndOut *atomic.Uintptr, log *slog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			log.Warn("WebView2 недоступен, открываю браузер", "panic", r)
			openBrowser(url)
		}
	}()

	w, h := windowSize()
	wv := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		DataPath:  dataPath,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  "TaskFlow — трекер задач",
			Width:  w,
			Height: h,
			Center: true,
			IconId: 1, // IDI_APPLICATION из resource_windows_amd64.syso
		},
	})
	if wv == nil {
		log.Warn("WebView2 не инициализировался, открываю браузер")
		openBrowser(url)
		return
	}
	defer wv.Destroy()
	if p := wv.Window(); p != nil {
		hwndOut.Store(uintptr(p))
	}

	// Отмена контекста (сигнал выхода / падение сервера) закрывает окно.
	go func() {
		<-ctx.Done()
		wv.Terminate() // безопасно вызывать из фоновой горутины
	}()

	wv.Navigate(url)
	wv.Run() // цикл сообщений; блокирует до закрытия окна
}

// windowSize подбирает размер окна под рабочий стол пользователя:
// ~86% ширины и ~90% высоты рабочей области, в разумных пределах.
func windowSize() (uint, uint) {
	sw, sh := screenWorkArea()
	if sw < 600 || sh < 400 {
		return 1440, 900 // метрики недоступны или мусорные — безопасный дефолт
	}
	w := clamp(int(float64(sw)*0.86), 1024, 1680)
	h := clamp(int(float64(sh)*0.90), 600, 1050)
	return uint(w), uint(h)
}

func clamp(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func openBrowser(url string) {
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

// defaultDBPath выбирает data/taskflow.db рядом с exe; если каталог
// read-only (например, Program Files), откатывается на %LOCALAPPDATA%.
func defaultDBPath(log *slog.Logger) string {
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Join(filepath.Dir(exe), "data")
		if err := os.MkdirAll(dir, 0o755); err == nil {
			probe := filepath.Join(dir, ".write-test")
			if f, err := os.Create(probe); err == nil {
				_ = f.Close()
				_ = os.Remove(probe)
				return filepath.Join(dir, "taskflow.db")
			}
		}
	}
	appData := os.Getenv("LOCALAPPDATA")
	if appData == "" {
		return "taskflow.db"
	}
	p := filepath.Join(appData, "TaskFlow", "taskflow.db")
	if log != nil {
		log.Info("каталог exe недоступен для записи, база в LOCALAPPDATA", "path", p)
	}
	return p
}

// newLogger пишет в stderr (видно в headless-режиме из терминала) и
// в taskflow.log рядом с базой.
func newLogger(dataDir string) *slog.Logger {
	writers := []io.Writer{os.Stderr}
	if f, err := os.OpenFile(filepath.Join(dataDir, "taskflow.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		writers = append(writers, f)
	}
	return slog.New(slog.NewTextHandler(io.MultiWriter(writers...), &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func eventTime(e domain.Event) time.Time {
	if e.Timestamp.IsZero() {
		return time.Now().UTC()
	}
	return e.Timestamp
}
