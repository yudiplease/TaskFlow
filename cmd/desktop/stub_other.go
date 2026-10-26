//go:build !windows

// Стаб для не-Windows сборок: десктоп-версия использует WebView2.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "TaskFlow Desktop доступен только на Windows")
	os.Exit(1)
}
