package insights

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestPeriodFloorCeil(t *testing.T) {
	at := time.Date(2025, 5, 17, 13, 47, 31, 250e6, time.UTC)

	tests := []struct {
		period, floor, ceil string
	}{
		{"500ms", "2025-05-17 13:47:31.000", "2025-05-17 13:47:31.500"},
		{"1msec", "2025-05-17 13:47:31.250", "2025-05-17 13:47:31.250"},
		{"30s", "2025-05-17 13:47:30.000", "2025-05-17 13:48:00.000"},
		{"2secs", "2025-05-17 13:47:30.000", "2025-05-17 13:47:32.000"},
		{"5m", "2025-05-17 13:45:00.000", "2025-05-17 13:50:00.000"},
		{"15mins", "2025-05-17 13:45:00.000", "2025-05-17 14:00:00.000"},
		{"1hr", "2025-05-17 13:00:00.000", "2025-05-17 14:00:00.000"},
		{"2hrs", "2025-05-17 12:00:00.000", "2025-05-17 14:00:00.000"},
		{"1.5h", "2025-05-17 13:30:00.000", "2025-05-17 15:00:00.000"},
		{"1d", "2025-05-17 00:00:00.000", "2025-05-18 00:00:00.000"},
		{"1days", "2025-05-17 00:00:00.000", "2025-05-18 00:00:00.000"},
		{"1w", "2025-05-15 00:00:00.000", "2025-05-22 00:00:00.000"},
		{"1mo", "2025-05-01 00:00:00.000", "2025-06-01 00:00:00.000"},
		{"2months", "2025-05-01 00:00:00.000", "2025-07-01 00:00:00.000"},
		{"1q", "2025-04-01 00:00:00.000", "2025-07-01 00:00:00.000"},
		{"1qtr", "2025-04-01 00:00:00.000", "2025-07-01 00:00:00.000"},
		{"1y", "2025-01-01 00:00:00.000", "2026-01-01 00:00:00.000"},
		{"1yrs", "2025-01-01 00:00:00.000", "2026-01-01 00:00:00.000"},
	}

	for _, tc := range tests {
		t.Run(tc.period, func(t *testing.T) {
			p, err := parsePeriod(&token{kind: tNumber, text: tc.period})
			if err != nil {
				t.Fatal(err)
			}

			if got := p.floor(at).Format(timestampLayout); got != tc.floor {
				t.Errorf("floor = %s, want %s", got, tc.floor)
			}

			if got := p.ceil(at).Format(timestampLayout); got != tc.ceil {
				t.Errorf("ceil = %s, want %s", got, tc.ceil)
			}
		})
	}
}

func TestParsePeriodRejects(t *testing.T) {
	for _, s := range []string{"5", "m", "0s", "5x", "5ss", "1.5mo", "1.2.3m", "100000000000000000000h", "2000y", "0.0001ms"} {
		if _, err := parsePeriod(&token{kind: tNumber, text: s}); err == nil {
			t.Errorf("parsePeriod(%q) accepted", s)
		}
	}

	if _, err := parsePeriod(&token{kind: tIdent, text: "5m"}); err == nil {
		t.Error("an identifier is not a period")
	}
}

func TestToTimeGuards(t *testing.T) {
	for _, v := range []any{math.NaN(), math.Inf(1), math.Inf(-1), 1e300, -1e300, "nan", "inf", "not a time", nil} {
		if _, ok := toTime(v); ok {
			t.Errorf("toTime(%v) accepted", v)
		}
	}

	got, ok := toTime(float64(1735689600000))
	if !ok || got.Format(timestampLayout) != "2025-01-01 00:00:00.000" {
		t.Fatalf("toTime(epoch ms) = %v, %v", got, ok)
	}
}

func TestDateFunctionsInFields(t *testing.T) {
	res := run(t, "fields bin(1m) as b, datefloor(@timestamp, 1h) as f, dateceil(@timestamp, 1h) as c, dateceil(latency, 1m) as x"+
		" | sort @timestamp asc | limit 1 | display b, f, c, x")

	got := strings.Join([]string{col(res.Rows, "b")[0], col(res.Rows, "f")[0], col(res.Rows, "c")[0], col(res.Rows, "x")[0]}, "|")
	if got != "2025-01-01 00:00:00.000|2025-01-01 00:00:00.000|2025-01-01 00:00:00.000|1970-01-01 00:01:00.000" {
		t.Fatalf("date functions = %s", got)
	}
}
