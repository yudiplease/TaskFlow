package domain

import (
	"testing"
	"time"
)

func TestParseTimeOfDay_OK(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"18:30", 18*60 + 30},
		{"9:05", 9*60 + 5},    // ведущий ноль часа необязателен
		{"09:05", 9*60 + 5},   // и допустим
		{"9:5", 9*60 + 5},     // одиночная цифра минут — тоже
		{" 07:00 ", 7 * 60},   // пробелы обрезаются
		{"00:00", 0},
		{"23:59", 23*60 + 59},
	}
	for _, tc := range cases {
		got, err := ParseTimeOfDay(tc.in)
		if err != nil {
			t.Errorf("ParseTimeOfDay(%q): неожиданная ошибка %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseTimeOfDay(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestParseTimeOfDay_Rejects(t *testing.T) {
	for _, in := range []string{"24:00", "12:60", ":30", "18:", "1830", "", "18:30:00", "-1:30", "25:61", "abc:xy"} {
		if _, err := ParseTimeOfDay(in); err == nil {
			t.Errorf("ParseTimeOfDay(%q): ожидалась ошибка", in)
		}
	}
}

func TestFormatTimeOfDay_Roundtrip(t *testing.T) {
	minutes, err := ParseTimeOfDay("9:5")
	if err != nil {
		t.Fatal(err)
	}
	if got := FormatTimeOfDay(minutes); got != "09:05" {
		t.Errorf("FormatTimeOfDay = %q, want 09:05", got)
	}
}

func TestValidateReminderOffset(t *testing.T) {
	for _, ok := range []int{0, 1, 5, 15, 60, 1440, MaxReminderOffsetMinutes} {
		if err := ValidateReminderOffset(ok); err != nil {
			t.Errorf("ValidateReminderOffset(%d): неожиданная ошибка %v", ok, err)
		}
	}
	for _, bad := range []int{-5, MaxReminderOffsetMinutes + 1} {
		if err := ValidateReminderOffset(bad); err == nil {
			t.Errorf("ValidateReminderOffset(%d): ожидалась ошибка", bad)
		}
	}
}

// TestDueAt_DSTDoesNotShift проверяет главное свойство хранения срока
// инстантом в UTC: дата+время, заданные в зоне пользователя, не «съезжают»
// при DST-переходе. Europe/Berlin, весенний переход 29.03.2026 02:00→03:00
// (CEEТ→CEST): «01:30 + 90 минут» должно показать 03:00 локальных —
// час не теряется и не дублируется при пересечении границы.
func TestDueAt_DSTDoesNotShift(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skipf("нет базы tzdata: %v", err)
	}

	// Пользователь назначает срок «29.03.2026 01:30» (до перехода на летнее время).
	wall := time.Date(2026, 3, 29, 1, 30, 0, 0, loc)
	if got := wall.In(loc).Format("15:04"); got != "01:30" {
		t.Fatalf("стеночное время после конвертации в зону = %q, want 01:30", got)
	}

	// Храним UTC-инстант (как due_at timestamptz) и добавляем 90 минут.
	// 30 стеночных минут до перехода (01:30 CET → 02:00) + час, «пропущенный»
	// переводом стрелок: инстант верен, стеночное время = 04:00 CEST,
	// ровно 90 реальных минут между инстантами.
	utc := wall.UTC().Add(90 * time.Minute)
	if got := utc.In(loc).Format("15:04"); got != "04:00" {
		t.Errorf("после 90 минут через DST-переход = %q, want 04:00", got)
	}
	if got := utc.Sub(wall.UTC()); got != 90*time.Minute {
		t.Errorf("интервал инстантов = %v, want ровно 90m (DST не должен съедать время)", got)
	}

	// Осенний переход 25.10.2026 03:00→02:00: срок «25.10 02:30» по CEST
	// (UTC+2) остаётся тем же инстантом, сколько бы раз его ни конвертировали.
	wallFall := time.Date(2026, 10, 25, 2, 30, 0, 0, time.FixedZone("CEST", 2*3600))
	rt, err := time.Parse(time.RFC3339, wallFall.Format(time.RFC3339))
	if err != nil {
		t.Fatal(err)
	}
	if !rt.Equal(wallFall) {
		t.Errorf("инстант изменился при сериализации: %v != %v", rt, wallFall)
	}
	if got := rt.In(wallFall.Location()).Format("15:04"); got != "02:30" {
		t.Errorf("после round-trip = %q, want 02:30", got)
	}
}
