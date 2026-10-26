package domain

import (
	"fmt"
	"strings"
)

// ColorTag — запись справочника цветовых меток задач.
// Справочник фиксированный: цвета нельзя добавлять через API,
// фронтенд берёт его целиком из GET /api/colors.
type ColorTag struct {
	Hex         string `json:"hex"`
	Emoji       string `json:"emoji"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// ColorTags — фиксированный справочник. Порядок = порядок в легенде и палитре.
var ColorTags = []ColorTag{
	{"#E53935", "🔴", "Критично / блокер", "Задача блокирует других, срок сорван"},
	{"#FB8C00", "🟠", "Срочно", "Дедлайн сегодня-завтра"},
	{"#FDD835", "🟡", "В работе", "Задача взята в работу"},
	{"#43A047", "🟢", "Готово", "Выполнено и проверено"},
	{"#1E88E5", "🔵", "В плане", "Запланировано на спринт"},
	{"#8E24AA", "🟣", "На ревью", "Ожидает проверки"},
	{"#9E9E9E", "⚪", "Бэклог", "Не приоритетно"},
	{"#212121", "⚫", "Архив", "Закрыто без выполнения"},
}

var colorByLower = func() map[string]ColorTag {
	m := make(map[string]ColorTag, len(ColorTags))
	for _, c := range ColorTags {
		m[strings.ToLower(c.Hex)] = c
	}
	return m
}()

// NormalizeColor приводит цвет к каноническому виду справочника.
// Пустая строка допустима и означает «без цвета».
func NormalizeColor(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	c, ok := colorByLower[strings.ToLower(s)]
	if !ok {
		return "", fmt.Errorf("%w: неизвестный цвет %q — допустимы только значения из справочника", ErrInvalid, s)
	}
	return c.Hex, nil
}

// ValidColor проверяет, что цвет принадлежит справочнику (или пуст).
func ValidColor(s string) bool {
	_, err := NormalizeColor(s)
	return err == nil
}

// ColorByHex возвращает запись справочника по каноническому HEX.
func ColorByHex(hex string) (ColorTag, bool) {
	c, ok := colorByLower[strings.ToLower(strings.TrimSpace(hex))]
	return c, ok
}
