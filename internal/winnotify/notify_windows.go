//go:build windows

// Package winnotify показывает системные уведомления Windows (тосты)
// через Shell_NotifyIcon — без PowerShell и внешних зависимостей.
// На Windows 10/11 балун рендерится как нативный тост с именем и
// иконкой приложения.
package winnotify

import (
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	nimAdd    = 0x00000000
	nimModify = 0x00000001
	nimDelete = 0x00000002

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nifInfo    = 0x00000010

	niifInfo = 0x00000001

	callbackMsg = 0x8000 // WM_APP — сообщения от значка (не обрабатываем)
)

type notifyIconData struct {
	CbSize           uint32 // обязателен первым полем: sizeof(NOTIFYICONDATA)
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32 // union: uTimeout / uVersion
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GUIDItem         windows.GUID
	HBalloonIcon     uintptr
}

var (
	shell32              = windows.NewLazySystemDLL("shell32.dll")
	user32               = windows.NewLazySystemDLL("user32.dll")
	procShellNotifyIcon  = shell32.NewProc("Shell_NotifyIconW")
	procLoadImageW       = user32.NewProc("LoadImageW")
	procGetModuleHandleW = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW")

	mu   sync.Mutex // Shell_NotifyIcon с одним UID не потокобезопасен
	uid  uint32     = 0x7F01
	once sync.Once
	appIcon uintptr
)

func stringToBuf(s string, buf []uint16) int {
	utf16, err := syscall.UTF16FromString(s)
	if err != nil {
		utf16 = []uint16{0}
	}
	n := copy(buf, utf16)
	if n == len(buf) {
		buf[n-1] = 0 // гарантия нуль-терминации
	}
	return n
}

// loadAppIcon загружает иконку приложения из ресурсов (ID 1),
// при неудаче — системную.
func loadAppIcon() uintptr {
	once.Do(func() {
		h, _, _ := procGetModuleHandleW.Call(0)
		icon, _, _ := procLoadImageW.Call(
			h, uintptr(1), // ресурс ID 1 (наша иконка)
			1, // IMAGE_ICON
			0, 0,
				0x40|0x8000, // LR_DEFAULTSIZE | LR_SHARED
		)
		if icon == 0 {
			icon, _, _ = procLoadImageW.Call(0, uintptr(32512), 1, 0, 0, 0x40|0x8000)
		}
		appIcon = icon
	})
	return appIcon
}

// Show отображает системное уведомление. owner — HWND окна приложения
// (может быть 0: тогда значок не регистрируется и уведомление не показывается).
func Show(owner uintptr, title, message string) error {
	if owner == 0 {
		return fmt.Errorf("нет окна-владельца для уведомления")
	}
	if len(title) == 0 || len(message) == 0 {
		return fmt.Errorf("пустой текст уведомления")
	}

	mu.Lock()
	defer mu.Unlock()

	data := &notifyIconData{
		CbSize:           uint32(unsafe.Sizeof(notifyIconData{})),
		HWnd:             owner,
		UID:              uid,
		UFlags:           nifMessage | nifIcon | nifTip,
		UCallbackMessage: callbackMsg,
		HIcon:            loadAppIcon(),
	}
	stringToBuf("TaskFlow", data.SzTip[:])

	ret, _, err := procShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(data)))
	if ret == 0 {
		return fmt.Errorf("добавление значка: %v", err)
	}

	// Показ балуна; значок держим до конца отображения, затем убираем.
	showData := *data
	showData.UFlags = nifInfo
	stringToBuf(title, showData.SzInfoTitle[:])
	stringToBuf(message, showData.SzInfo[:])
	showData.DwInfoFlags = niifInfo
	showData.UVersion = 10000 // 10 секунд

	ret, _, err = procShellNotifyIcon.Call(nimModify, uintptr(unsafe.Pointer(&showData)))
	if ret == 0 {
		_, _, _ = procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(data)))
		return fmt.Errorf("показ уведомления: %v", err)
	}

	// Убираем значок через 12 секунд (тост уже показан оболочкой).
	go func(d notifyIconData) {
		<-time.After(12 * time.Second)
		mu.Lock()
		defer mu.Unlock()
		_, _, _ = procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&d)))
	}(showData)
	return nil
}
