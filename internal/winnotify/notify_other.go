//go:build !windows

// Package winnotify — стаб для платформ, где системные тосты не реализованы.
package winnotify

import "fmt"

// Show на не-Windows системах сообщает, что тосты недоступны.
func Show(owner uintptr, title, message string) error {
	return fmt.Errorf("системные уведомления поддерживаются только на Windows")
}
