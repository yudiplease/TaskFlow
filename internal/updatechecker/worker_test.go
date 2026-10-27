package updatechecker

import (
	"testing"
	"time"
)

func TestCheckDue(t *testing.T) {
	now := time.Now()
	hourAgo := now.Add(-time.Hour)
	twoDaysAgo := now.Add(-48 * time.Hour)
	inTwoHours := now.Add(2 * time.Hour)

	cases := []struct {
		name     string
		st       *UpdateState
		interval time.Duration
		want     bool // true — проверять пора
	}{
		{
			name:     "первый запуск (state нет) — проверяем",
			st:       nil,
			interval: 24 * time.Hour,
			want:     true,
		},
		{
			name:     "последняя проверка час назад при интервале 24ч — рано",
			st:       &UpdateState{Enabled: true, LastCheckedAt: &hourAgo},
			interval: 24 * time.Hour,
			want:     false,
		},
		{
			name:     "последняя проверка 48ч назад при интервале 24ч — пора",
			st:       &UpdateState{Enabled: true, LastCheckedAt: &twoDaysAgo},
			interval: 24 * time.Hour,
			want:     true,
		},
		{
			name:     "авто-проверка выключена",
			st:       &UpdateState{Enabled: false, LastCheckedAt: &twoDaysAgo},
			interval: 24 * time.Hour,
			want:     false,
		},
		{
			name:     "пауза после rate-limit не истекла",
			st:       &UpdateState{Enabled: true, NextCheckAfter: &inTwoHours},
			interval: 24 * time.Hour,
			want:     false,
		},
	}
	for _, c := range cases {
		err := checkDue(c.st, c.interval, now)
		got := err == nil
		if got != c.want {
			t.Errorf("%s: checkDue = %v (err=%v), хочу %v", c.name, got, err, c.want)
		}
	}
}
