package updatechecker

import "testing"

func TestIsValidVersion(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"v1.3.2", true},
		{"1.3.2", true},
		{"dev", false}, // dev-сборки не проверяются
		{"", false},
		{"v1.3", false},
		{"v1.3.2.1", false},
		{"main", false},
		{"v1.3.2-rc1", true},
	}
	for _, c := range cases {
		if got := IsValidVersion(c.v); got != c.want {
			t.Errorf("IsValidVersion(%q) = %v, хочу %v", c.v, got, c.want)
		}
	}
}

// Сравнение числовое по полям, не строковое; пререлиз младше релиза.
func TestIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v1.4.0", "v1.3.2", true},      // новая версия
		{"v1.3.2", "v1.3.2", false},     // та же
		{"v1.3.1", "v1.3.2", false},     // старее
		{"v1.10.0", "v1.9.0", true},     // числовое сравнение, не строковое
		{"v2.0.0", "v1.9.9", true},      // мажорная
		{"v1.4.0-rc1", "v1.4.0", false}, // пререлиз младше релиза
		{"v1.4.0", "v1.4.0-rc1", true},  // релиз новее своего пререлиза
		{"v1.4.0-rc2", "v1.4.0-rc1", true},
		{"not-a-version", "v1.3.2", false}, // тег не semver
		{"v1.4.0", "dev", false},           // dev-сборка
		{"", "v1.3.2", false},
	}
	for _, c := range cases {
		if got := IsNewer(c.latest, c.current); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, хочу %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestShouldNotify(t *testing.T) {
	on := &UpdateState{Enabled: true, IntervalHours: 24}
	off := &UpdateState{Enabled: false, IntervalHours: 24}

	cases := []struct {
		name, latest, app string
		state             *UpdateState
		want              bool
	}{
		{"новее → true", "v1.4.0", "v1.3.2", on, true},
		{"равны → false", "v1.3.2", "v1.3.2", on, false},
		{"старее → false", "v1.3.1", "v1.3.2", on, false},
		{"dev-сборка → false", "v1.4.0", "dev", on, false},
		{"невалидный тег → false", "кривой-тег", "v1.3.2", on, false},
		{"авто-проверка выключена → false", "v1.4.0", "v1.3.2", off, false},
		{"state nil → false", "v1.4.0", "v1.3.2", nil, false},
	}
	for _, c := range cases {
		if got := ShouldNotify(c.latest, c.app, c.state); got != c.want {
			t.Errorf("%s: ShouldNotify(%q, %q) = %v, хочу %v", c.name, c.latest, c.app, got, c.want)
		}
	}
}
