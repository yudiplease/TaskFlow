//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

type workRect struct{ L, T, R, B int32 }

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	procSystemParametersInf = user32.NewProc("SystemParametersInfoW")
)

const (
	smCXScreen     = 0 // ширина экрана
	smCYScreen     = 1 // высота экрана
	spiGetWorkArea = 0x30
	taskbarReserve = 48 // примерная высота панели задач для фолбэка
)

// screenWorkArea возвращает размеры рабочей области основного экрана.
// Основной источник — SPI_GETWORKAREA: метрика SM_CYFULLSCREEN на части
// систем (например, при сбойном состоянии оболочки) вывает мусор вроде 34px.
func screenWorkArea() (int, int) {
	var r workRect
	ret, _, _ := procSystemParametersInf.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&r)), 0)
	if ret != 0 && r.R > r.L && r.B > r.T {
		return int(r.R - r.L), int(r.B - r.T)
	}
	// Фолбэк: полный экран минус типовая панель задач.
	w, _, _ := procGetSystemMetrics.Call(smCXScreen)
	h, _, _ := procGetSystemMetrics.Call(smCYScreen)
	sw, sh := int(int32(w)), int(int32(h))
	if sw > 0 && sh > taskbarReserve {
		return sw, sh - taskbarReserve
	}
	return 0, 0 // метрики недоступны — вызывающий использует дефолт
}
