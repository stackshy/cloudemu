package insights

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func sampleRecords() []Record {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	msgs := []string{
		`{"level":"ERROR","latency":120,"user":{"id":"u1"},"tags":["a","b"]}`,
		`{"level":"INFO","latency":15,"user":{"id":"u2"}}`,
		"plain ERROR text Exception happened",
		`{"level":"WARN","latency":80}`,
	}

	recs := make([]Record, 0, len(msgs))
	for i, m := range msgs {
		recs = append(recs, NewRecord(&Event{
			Account: "123456789012", LogGroup: "/app", LogStream: "s",
			Timestamp: base.Add(time.Duration(i) * time.Second), Ingestion: base, Message: m, Ptr: "p" + string(rune('0'+i)),
		}))
	}

	return recs
}

func run(t *testing.T, q string) Result {
	t.Helper()

	parsed, err := Parse(q)
	if err != nil {
		t.Fatalf("Parse(%q): %v", q, err)
	}

	return parsed.Run(sampleRecords(), 0)
}

func col(rows [][]Field, name string) []string {
	var out []string

	for _, row := range rows {
		for _, f := range row {
			if f.Name == name {
				out = append(out, f.Value)
			}
		}
	}

	return out
}

func TestQueries(t *testing.T) {
	tests := []struct {
		q     string
		field string
		want  string
	}{
		{"fields @timestamp, @message | sort @timestamp desc | limit 2", "@timestamp",
			"2025-01-01 00:00:03.000|2025-01-01 00:00:02.000"},
		{"fields level | filter level = 'ERROR'", "level", "ERROR"},
		{"filter latency > 50 | sort latency asc | fields latency", "latency", "80|120"},
		{"filter latency >= 15 and level != 'WARN' | sort latency | fields latency", "latency", "120|15"},
		{"filter @message like /Exception/ | fields @message", "@message", "plain ERROR text Exception happened"},
		{`filter @message like "ERROR" | fields @logStream`, "@logStream", "s|s"},
		{"filter @message not like /ERROR/ | fields level", "level", "WARN|INFO"},
		{"filter level in ['WARN', 'INFO'] | fields level", "level", "WARN|INFO"},
		{"filter level not in ['WARN', 'INFO'] and ispresent(level) | fields level", "level", "ERROR"},
		{"filter level =~ /^E/ | fields user.id", "user.id", "u1"},
		{"filter not ispresent(level) | fields @log", "@log", "123456789012:/app"},
		{"filter tags.1 = 'b' | fields tags.0", "tags.0", "a"},
		{"fields latency * 2 as double | filter double > 200 | display double", "double", "240"},
		{"fields toupper(level) as lv, strlen(level) as n | filter n = 4 | sort lv asc | display lv", "lv", "INFO|WARN"},
		{"fields concat(level, '-', user.id) as k | filter ispresent(user.id) | sort k asc | display k", "k", "ERROR-u1|INFO-u2"},
		{"# comment\nfields level | limit 1", "level", "WARN"},
		{"filter (latency < 20 or latency > 100) | fields abs(latency - 100) as d | sort d asc | display d", "d", "20|85"},
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

func TestDefaultColumnsPtrAndMatched(t *testing.T) {
	res := run(t, "filter ispresent(latency) | limit 1")

	if res.Matched != 3 {
		t.Fatalf("Matched = %d, want 3 (before the limit)", res.Matched)
	}

	if len(res.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(res.Rows))
	}

	var names []string
	for _, f := range res.Rows[0] {
		names = append(names, f.Name)
	}

	if strings.Join(names, ",") != "@timestamp,@message,@ptr" {
		t.Fatalf("columns = %v, want @timestamp, @message and @ptr", names)
	}
}

func TestRunLimitParam(t *testing.T) {
	q, err := Parse("fields @message")
	if err != nil {
		t.Fatal(err)
	}

	if got := len(q.Run(sampleRecords(), 2).Rows); got != 2 {
		t.Fatalf("rows = %d, want 2", got)
	}
}

func TestCompileErrors(t *testing.T) {
	for _, q := range []string{
		"",
		"fields",
		"filter latency >",
		"filter level = 'x",
		"pattern @message",
		"fields level |",
		"filter nosuch(level)",
		"sort",
		"limit x",
		"filter level in 'a'",
		"filter @message like /unterminated",
		"fields level as",
		"filter (level = 'x'",
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

func TestFieldShares(t *testing.T) {
	shares := FieldShares(sampleRecords())

	got := map[string]int{}
	for _, s := range shares {
		got[s.Name] = s.Percent
	}

	if got["@message"] != 100 || got["level"] != 75 || got["user.id"] != 50 || got["tags.0"] != 25 {
		t.Fatalf("shares = %v", got)
	}

	if _, ok := got["@ptr"]; ok {
		t.Fatal("@ptr listed as a field")
	}

	if shares[0].Percent != 100 {
		t.Fatalf("not sorted by percent: %v", shares)
	}
}
