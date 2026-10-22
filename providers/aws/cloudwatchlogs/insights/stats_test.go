package insights

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

// eventsAt builds records for messages logged at the given offsets from
// midnight 2025-01-01 UTC.
func eventsAt(stream string, msgs []string, offsets []time.Duration) []Record {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	recs := make([]Record, 0, len(msgs))
	for i, m := range msgs {
		recs = append(recs, NewRecord(&Event{
			Account: "123456789012", LogGroup: "/app", LogStream: stream,
			Timestamp: base.Add(offsets[i]), Ingestion: base, Message: m, Ptr: "p" + strconv.Itoa(i),
		}))
	}

	return recs
}

func runOn(t *testing.T, q string, recs []Record) Result {
	t.Helper()

	parsed, err := Parse(q)
	if err != nil {
		t.Fatalf("Parse(%q): %v", q, err)
	}

	return parsed.Run(recs, 0)
}

func TestStatsFunctions(t *testing.T) {
	// The sample latencies are 120, 15 and 80; levels ERROR, INFO, WARN
	// and one event without a level.
	mean := (120.0 + 15 + 80) / 3
	sd := math.Sqrt((math.Pow(120-mean, 2) + math.Pow(15-mean, 2) + math.Pow(80-mean, 2)) / 3)

	tests := []struct {
		q, field, want string
	}{
		{"stats count(*)", "count(*)", "4"},
		{"stats count()", "count()", "4"},
		{"stats count(level) as n", "n", "3"},
		{"stats count_distinct(level) as n", "n", "3"},
		{"stats countDistinct(user.id) as n", "n", "2"},
		{"stats sum(latency) as s", "s", "215"},
		{"stats avg(latency) as a", "a", strconv.FormatFloat(mean, 'f', -1, 64)},
		{"stats min(latency) as m", "m", "15"},
		{"stats max(latency) as m", "m", "120"},
		{"stats min(level) as m", "m", "ERROR"},
		{"stats pct(latency, 50) as p", "p", "80"},
		{"stats pct(latency, 0) as p", "p", "15"},
		{"stats pct(latency, 100) as p", "p", "120"},
		{"stats pct(latency, 99.9) as p", "p", "120"},
		{"stats stddev(latency) as sd", "sd", strconv.FormatFloat(sd, 'f', -1, 64)},
		{"stats earliest(level) as e", "e", "ERROR"},
		{"stats latest(level) as l", "l", "WARN"},
		{"stats sortsFirst(level) as f", "f", "ERROR"},
		{"stats sortsLast(level) as l", "l", "WARN"},
		{"stats sum(latency) / count(*) as x", "x", "53.75"},
		{"stats round(avg(latency)) as r", "r", "72"},
		{"stats pct(latency, 50)", "pct(latency, 50)", "80"},
		{"stats count(*) as n, max(latency) as top", "top", "120"},
		{"stats sum(nosuch) as s", "s", ""},
		{"filter ispresent(latency) | stats count(*) as n by level | sort level asc", "level", "ERROR|INFO|WARN"},
		{"stats count(*) as n by level | sort level asc", "level", "ERROR|INFO|WARN"},
		{"stats count(*) as n by level | sort level asc", "n", "1|1|1|1"},
		{"stats count(*) as n by toupper(level) as lv | filter lv = 'INFO'", "lv", "INFO"},
		{"stats count(*) as n by level | stats sum(n) as total", "total", "4"},
		{"stats sum(latency) as s by level | filter s > 50 | stats count(*) as big", "big", "2"},
		{"stats max(latency) as m by level | filter ispresent(m) | sort m desc | limit 2", "m", "120|80"},
		{"stats max(latency) as m by level | fields m * 2 as d | sort d asc | display d", "d", "30|160|240"},
		{"STATS count(*) AS n BY level | SORT n DESC | LIMIT 1", "n", "1"},
	}

	for _, tc := range tests {
		t.Run(tc.q, func(t *testing.T) {
			got := strings.Join(col(run(t, tc.q).Rows, tc.field), "|")
			if got != tc.want {
				t.Fatalf("%s = %q, want %q", tc.field, got, tc.want)
			}
		})
	}
}

func TestStatsRowShape(t *testing.T) {
	res := run(t, "filter ispresent(latency) | stats count(*), avg(latency) by level | sort level asc")

	if res.Matched != 3 {
		t.Fatalf("Matched = %d, want the 3 records that reached stats", res.Matched)
	}

	if len(res.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(res.Rows))
	}

	var names []string
	for _, f := range res.Rows[0] {
		names = append(names, f.Name)
	}

	if got := strings.Join(names, ","); got != "level,count(*),avg(latency)" {
		t.Fatalf("columns = %s, want the by field then the aggregations and no @ptr", got)
	}
}

func TestStatsNoRecords(t *testing.T) {
	if res := run(t, "filter level = 'nope' | stats count(*)"); len(res.Rows) != 0 {
		t.Fatalf("rows = %v, want none", res.Rows)
	}
}

func TestStatsBin(t *testing.T) {
	msgs := []string{`{"v":1}`, `{"v":2}`, `{"v":4}`, `{"v":8}`}
	offsets := []time.Duration{time.Minute, 4 * time.Minute, 6 * time.Minute, 11*time.Minute + 59*time.Second}
	recs := func() []Record { return eventsAt("s", msgs, offsets) }

	res := runOn(t, "stats count(*) as n, sum(v) as s by bin(5m)", recs())
	if got := strings.Join(col(res.Rows, "bin(5m)"), "|"); got != "2025-01-01 00:10:00.000|2025-01-01 00:05:00.000|2025-01-01 00:00:00.000" {
		t.Fatalf("bins = %s, want newest first", got)
	}

	if got := strings.Join(col(res.Rows, "s"), "|"); got != "8|4|3" {
		t.Fatalf("sums = %s", got)
	}

	res = runOn(t, "stats sum(v) as s by bin(1h) as hour", recs())
	if got := strings.Join(col(res.Rows, "hour"), "|") + "=" + strings.Join(col(res.Rows, "s"), "|"); got != "2025-01-01 00:00:00.000=15" {
		t.Fatalf("hourly = %s", got)
	}

	res = runOn(t, "stats sum(v) as s by bin(5m) as @t | stats max(s) as peak, avg(s) as mean by dateceil(@t, 10m) as w | sort w asc",
		recs())
	if got := strings.Join(col(res.Rows, "w"), "|") + "=" + strings.Join(col(res.Rows, "peak"), "|"); got !=
		"2025-01-01 00:00:00.000|2025-01-01 00:10:00.000=3|8" {
		t.Fatalf("chained bins = %s", got)
	}
}

func TestStatsCompileErrors(t *testing.T) {
	tooMany := strings.TrimSuffix(strings.Repeat("stats count(*) as n | ", maxStatsCommands+1), " | ")
	tenOK := strings.TrimSuffix(strings.Repeat("stats count(*) as n | ", maxStatsCommands), " | ")

	if _, err := Parse(tenOK); err != nil {
		t.Fatalf("10 stats commands: %v", err)
	}

	for _, q := range []string{
		"stats",
		"stats level",
		"stats count(*) by",
		"stats count(*) as",
		"stats count(*) | sort n | stats count(*)",
		"limit 5 | stats count(*)",
		"stats count(*) by level | stats max(latency)",
		"stats sum(latency) as s by @logStream | stats avg(s) by bin(5m)",
		"stats count(*) as n | filter level = 'x'",
		"stats count(*) as n | fields @message",
		"stats count(*) as n | sort level",
		"filter count(*) > 1",
		"fields sum(latency)",
		"stats sum(count(*))",
		"stats count(**)",
		"stats sum(*)",
		"stats pct(latency)",
		"stats pct(latency, 101)",
		"stats pct(latency, x)",
		"stats count(*) by bin(5x)",
		"stats count(*) by bin(0m)",
		"stats count(*) by bin(m)",
		"stats count(*) by bin(1.5mo)",
		"stats count(*) by bin(99999999999999999999h)",
		"stats count(*) by bin()",
		"stats count(*) by bin(5m",
		"stats count(*) by datefloor(@timestamp)",
		tooMany,
	} {
		_, err := Parse(q)

		var ce *CompileError
		if !errors.As(err, &ce) {
			t.Errorf("Parse(%q) = %v, want a CompileError", q, err)
			continue
		}

		if ce.Start < 0 || ce.End < ce.Start || ce.End > len(q) {
			t.Errorf("Parse(%q) offsets %d..%d out of range", q, ce.Start, ce.End)
		}
	}
}

func TestStatsChainingAllowed(t *testing.T) {
	for _, q := range []string{
		"stats sum(latency) as s, max(@timestamp) as @t by @logStream | stats avg(s) by dateceil(@t, 5m)",
		"stats count(*) as n by level | fields n + 1 as m | stats sum(m) as total",
		"stats count(*) as n by level | parse level /(?<first>.)/ | stats count(*) by first",
		"stats count(*) as n by level | sort n desc | limit 2",
	} {
		if _, err := Parse(q); err != nil {
			t.Errorf("Parse(%q): %v", q, err)
		}
	}
}
