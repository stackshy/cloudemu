package insights

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func serverRecords() []Record {
	return eventsAt("s", []string{
		`{"server":"s1","n":1}`,
		`{"server":"s2","n":2}`,
		`{"server":"s1","n":3}`,
		`{"n":4}`,
		`{"n":5}`,
	}, []time.Duration{0, time.Second, 2 * time.Second, 3 * time.Second, 4 * time.Second})
}

func TestDedup(t *testing.T) {
	tests := []struct {
		q, want string
	}{
		// Default newest-first order keeps the latest s1; events without a
		// server are all kept.
		{"fields n | dedup server", "5|4|3|2"},
		{"fields n | sort @timestamp asc | dedup server", "1|2|4|5"},
		{"fields n | sort n desc | dedup server | limit 2", "5|4"},
		{"fields n | filter ispresent(server) | dedup server, n", "3|2|1"},
		{"fields n | dedup @logStream", "5"},
	}

	for _, tc := range tests {
		t.Run(tc.q, func(t *testing.T) {
			if got := strings.Join(col(runOn(t, tc.q, serverRecords()).Rows, "n"), "|"); got != tc.want {
				t.Fatalf("n = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDedupMatchedCountsBeforeDedup(t *testing.T) {
	res := runOn(t, "fields n | dedup server", serverRecords())
	if res.Matched != 5 || len(res.Rows) != 4 {
		t.Fatalf("matched %d rows %d, want 5 and 4", res.Matched, len(res.Rows))
	}
}

func TestDedupCompileErrors(t *testing.T) {
	for _, q := range []string{
		"dedup",
		"dedup server,",
		"dedup server | sort n",
		"dedup server | fields n",
		"dedup server | stats count(*)",
		"stats count(*) as c | dedup server",
	} {
		var ce *CompileError
		if _, err := Parse(q); !errors.As(err, &ce) {
			t.Errorf("Parse(%q) = %v, want a CompileError", q, err)
		}
	}
}
