//go:build windows

// Озвучка Аси: WebView2/Edge не видят SAPI-голоса (RHVoice и прочие классические)
// — перечисляет только OneCore и «Online (Natural)». Поэтому синтез делаем на
// нашей стороне: PowerShell + System.Speech (RHVoice Елена), результат — WAV
// в кэше на диске, фронтенд проигрывает его элементом <audio>.
package main

import (
	"context"
	"crypto/sha1"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed tts/synth.ps1
var ttsScript []byte

const ttsMaxTextLen = 1000

// Одновременно один синтез: System.Speech и RHVoice не рассчитаны на параллельный запуск.
var ttsMu sync.Mutex

type ttsRequest struct {
	Text string `json:"text"`
}

// ttsHandler отдаёт POST /api/tts {text} -> audio/wav.
func ttsHandler(dataDir string, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
		if err != nil {
			http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
			return
		}
		var req ttsRequest
		if json.Unmarshal(body, &req) != nil {
			http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
			return
		}
		text := strings.TrimSpace(req.Text)
		if text == "" || len(text) > ttsMaxTextLen {
			http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
			return
		}

		cacheDir := filepath.Join(dataDir, "tts-cache")
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			http.Error(w, `{"error":"tts failed"}`, http.StatusInternalServerError)
			return
		}
		sum := sha1.Sum([]byte(text))
		outPath := filepath.Join(cacheDir, hex.EncodeToString(sum[:])+".wav")

		if _, err := os.Stat(outPath); err != nil {
			ttsMu.Lock()
			err = synthToFile(text, dataDir, outPath, log)
			ttsMu.Unlock()
			if err != nil {
				log.Error("синтез речи", "err", err)
				http.Error(w, `{"error":"tts failed"}`, http.StatusInternalServerError)
				return
			}
		}
		w.Header().Set("Content-Type", "audio/wav")
		http.ServeFile(w, r, outPath)
	}
}

func synthToFile(text, dataDir, outPath string, log *slog.Logger) error {
	scriptPath := filepath.Join(dataDir, "tts-synth.ps1")
	if err := os.WriteFile(scriptPath, ttsScript, 0o644); err != nil {
		return err
	}
	// Текст передаётся файлом: кириллица в аргументах командной строки — лотерея кодировок.
	inPath := outPath + ".in"
	if err := os.WriteFile(inPath, []byte(text), 0o644); err != nil {
		return err
	}
	defer os.Remove(inPath)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass",
		"-File", scriptPath, "-InFile", inPath, "-OutFile", outPath)
	// Приложение собрано с -H windowsgui: запуск консольного powershell
	// без этого флага открывает видимую командную строку.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if outBytes, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("powershell: %v: %s", err, string(outBytes))
	}
	if _, err := os.Stat(outPath); err != nil {
		return fmt.Errorf("powershell не создал wav")
	}
	return nil
}
