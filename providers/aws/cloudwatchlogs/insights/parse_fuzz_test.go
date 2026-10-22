package insights

import (
	"errors"
	"testing"
)

// FuzzParse checks that any query either compiles to a CompileError with
// in-range offsets or runs without panicking.
func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"fields @timestamp, @message | sort @timestamp desc | limit 20",
		"filter latency > 50 and level in ['WARN', 'INFO'] | fields substr(@message, 0, 5) as s",
		"stats count(*), avg(latency), pct(latency, 95) by level, bin(5m)",
		"stats sum(latency) as s by bin(5m) as @t | stats max(s) by dateceil(@t, 10m)",
		"stats count_distinct(user.id) as u, stddev(latency), earliest(level), sortsLast(level) | sort u desc",
		`parse @message "user=* action=*" as u, a | stats count(*) by a`,
		`parse @message /(?<code>\d+)/ | dedup code | limit 3`,
		"fields bin(1mo) as m, datefloor(latency, 1q) as q | dedup m",
		"stats count(*) by bin(1e)",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, q string) {
		parsed, err := Parse(q)
		if err != nil {
			var ce *CompileError
			if !errors.As(err, &ce) {
				t.Fatalf("Parse(%q) = %v, not a CompileError", q, err)
			}

			if ce.Start < 0 || ce.End < ce.Start || ce.End > len(q) {
				t.Fatalf("Parse(%q) offsets %d..%d out of range", q, ce.Start, ce.End)
			}

			return
		}

		parsed.Run(sampleRecords(), 0)
		parsed.Run(accessRecords(), 1)
	})
}
