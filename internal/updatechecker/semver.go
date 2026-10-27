package updatechecker

import "strings"

// semver — распарсенная версия вида vMAJOR.MINOR.PATCH[-PRERELEASE].
type semver struct {
	major, minor, patch int
	pre                 string // без ведущего '-' ('' — обычный релиз)
}

// parseSemver разбирает тег релиза. Принимает опциональное ведущее «v»,
// требует ровно три числовые части; prerelease — после «-».
func parseSemver(s string) (semver, bool) {
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return semver{}, false
	}
	core, pre, hasPre := strings.Cut(s, "-")
	if hasPre && pre == "" {
		return semver{}, false
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	var v semver
	var ok bool
	if v.major, ok = parseUint(parts[0]); !ok {
		return semver{}, false
	}
	if v.minor, ok = parseUint(parts[1]); !ok {
		return semver{}, false
	}
	if v.patch, ok = parseUint(parts[2]); !ok {
		return semver{}, false
	}
	v.pre = pre
	return v, true
}

func parseUint(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// compareSemver возвращает -1/0/1. Пререлиз младше релиза той же версии
// (v1.4.0-rc1 < v1.4.0); идентификаторы пререлиза сравниваются по правилам
// semver: числовые — численно, прочие — лексически, числовый < нечислового.
func compareSemver(a, b semver) int {
	if a.major != b.major {
		return cmpInt(a.major, b.major)
	}
	if a.minor != b.minor {
		return cmpInt(a.minor, b.minor)
	}
	if a.patch != b.patch {
		return cmpInt(a.patch, b.patch)
	}
	switch {
	case a.pre == "" && b.pre == "":
		return 0
	case a.pre == "":
		return 1 // релиз старше пререлиза
	case b.pre == "":
		return -1
	}
	return comparePre(a.pre, b.pre)
}

func comparePre(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if an, aok := parseUint(as[i]); aok {
			if bn, bok := parseUint(bs[i]); bok {
				if an != bn {
					return cmpInt(an, bn)
				}
				continue // равные числовые идентификаторы
			}
			return -1 // числовый идентификатор младше нечислового
		}
		if _, bok := parseUint(bs[i]); bok {
			return 1
		}
		if as[i] != bs[i] {
			if as[i] < bs[i] {
				return -1
			}
			return 1
		}
	}
	return cmpInt(len(as), len(bs)) // общий префикс равен — короче список младше
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// IsValidVersion сообщает, годится ли строка как версия установленного
// приложения. "dev" и прочий мусор → false: воркер на таких сборках не запускается.
func IsValidVersion(v string) bool {
	_, ok := parseSemver(v)
	return ok
}

// IsNewer сообщает, что latest строго новее установленной версии.
// Сравнение числовое по полям, не строковое: v1.10.0 > v1.9.0.
// Невалидные теги (включая пустые) → false.
func IsNewer(latest, current string) bool {
	l, ok := parseSemver(latest)
	if !ok {
		return false
	}
	c, ok := parseSemver(current)
	if !ok {
		return false
	}
	return compareSemver(l, c) > 0
}

// ShouldNotify — чистая функция решения «показывать ли баннер».
// latest — тег из кэша релиза, appVersion — версия приложения, state —
// настройки проверки.
//
// Правила MVP:
//   - авто-проверка выключена (state.Enabled=false) → false: фоновых
//     баннеров нет; явная кнопка «Проверить сейчас» идёт мимо этой функции
//     (пользователь сам попросил — ответ показываем всегда);
//   - тег не парсится как semver → false (баннер не показываем);
//   - версия приложения "dev"/невалидна → false;
//   - latest <= установленной (включая равенство) → false.
func ShouldNotify(latest, appVersion string, state *UpdateState) bool {
	if state == nil || !state.Enabled {
		return false
	}
	if !IsValidVersion(appVersion) {
		return false
	}
	return IsNewer(latest, appVersion)
}
