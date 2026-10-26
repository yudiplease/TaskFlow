//go:build !windows

package main

// screenWorkArea на не-Windows системах недоступен — окно получает дефолт.
func screenWorkArea() (int, int) { return 0, 0 }
