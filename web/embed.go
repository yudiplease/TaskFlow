// Package web содержит встроенные статические файлы интерфейса.
package web

import "embed"

//go:embed static
var Static embed.FS
