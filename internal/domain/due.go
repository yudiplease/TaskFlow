package domain

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseTimeOfDay разбирает время суток «ЧЧ:ММ» и возвращает число минут
// от полуночи. Ведущий ноль часа необязателен («9:05» = «09:05»);
// одиночная цифра минут тоже допускается («9:5» = «09:05»).
// Невозможные значения отклоняются: «24:00», «12:60», пустой час при
// заполненной минуте («:30»), лишние части («18:30:00»). Некорректное
// время не должно молча попадать в срок или sticky-значения.
func ParseTimeOfDay(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("%w: пустое время, ожидается формат ЧЧ:ММ", ErrInvalid)
	}
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("%w: время должно быть в формате ЧЧ:ММ, получено %q", ErrInvalid, s)
	}
	hh, mm := parts[0], parts[1]
	if hh == "" {
		return 0, fmt.Errorf("%w: пустой час при заполненной минуте (%q)", ErrInvalid, s)
	}
	if mm == "" {
		return 0, fmt.Errorf("%w: пустые минуты (%q)", ErrInvalid, s)
	}
	h, err := strconv.Atoi(hh)
	if err != nil || h < 0 {
		return 0, fmt.Errorf("%w: некорректный час %q", ErrInvalid, hh)
	}
	m, err := strconv.Atoi(mm)
	if err != nil || m < 0 {
		return 0, fmt.Errorf("%w: некорректные минуты %q", ErrInvalid, mm)
	}
	if h > 23 {
		return 0, fmt.Errorf("%w: час %d вне диапазона 0–23 (%q)", ErrInvalid, h, s)
	}
	if m > 59 {
		return 0, fmt.Errorf("%w: минуты %d вне диапазона 0–59 (%q)", ErrInvalid, m, s)
	}
	return h*60 + m, nil
}

// FormatTimeOfDay канонизирует время суток: «09:05», без ведущих пробелов.
func FormatTimeOfDay(minutes int) string {
	if minutes < 0 {
		minutes = 0
	}
	return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
}

// MaxReminderOffsetMinutes — верхняя граница «напомнить за N минут»:
// неделя. Больше смысла не имеет, а гигантские значения ломают планировщик.
const MaxReminderOffsetMinutes = 7 * 24 * 60

// ValidateReminderOffset проверяет «напомнить за N минут»: 0 = «не напоминать»
// (значение не сохраняется), иначе 1..MaxReminderOffsetMinutes.
func ValidateReminderOffset(minutes int) error {
	if minutes < 0 {
		return fmt.Errorf("%w: смещение напоминания не может быть отрицательным", ErrInvalid)
	}
	if minutes > MaxReminderOffsetMinutes {
		return fmt.Errorf("%w: напоминание больше чем за 7 дней не поддерживается", ErrInvalid)
	}
	return nil
}
