package vtl

import (
	"math"
	"testing"
)

func TestToIntRange(t *testing.T) {
	cases := []struct {
		name   string
		in     any
		want   int
		wantOK bool
	}{
		{"int64 in range", int64(42), 42, true},
		{"int64 max int32", int64(math.MaxInt32), math.MaxInt32, true},
		{"int64 min int32", int64(math.MinInt32), math.MinInt32, true},
		{"int64 above int32", int64(math.MaxInt32) + 1, 0, false},
		{"int64 below int32", int64(math.MinInt32) - 1, 0, false},
		{"int64 max", int64(math.MaxInt64), 0, false},
		{"float truncates", 1.9, 1, true},
		{"negative float truncates", -1.9, -1, true},
		{"float above int32", 1e19, 0, false},
		{"float below int32", -1e19, 0, false},
		{"NaN", math.NaN(), 0, false},
		{"positive infinity", math.Inf(1), 0, false},
		{"negative infinity", math.Inf(-1), 0, false},
		{"string", "1", 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := toInt(tc.in)
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("toInt(%v) = (%d, %v), want (%d, %v)", tc.in, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestOutOfRangeNumbersInTemplates checks that every place a template number
// becomes an index, offset or range bound treats an out-of-range value, NaN or
// an infinity as a non-integer instead of wrapping or saturating it.
func TestOutOfRangeNumbersInTemplates(t *testing.T) {
	vars := map[string]any{
		"big":  1e19,
		"huge": int64(math.MaxInt64),
		"wide": int64(math.MaxInt32) + 1,
		"nan":  math.NaN(),
		"inf":  math.Inf(1),
		"s":    "hello",
	}

	cases := []struct {
		name, src, want string
	}{
		{"range float bound", "#foreach($i in [0..$big])x#end.", "."},
		{"range int64 bound", "#foreach($i in [$huge..$huge])x#end.", "."},
		{"range NaN bound", "#foreach($i in [0..$nan])x#end.", "."},
		{"index read", "#set($l = [1, 2, 3])[$!l[$wide]]", "[]"},
		{"list get", "#set($l = [1, 2, 3])[$!l.get($huge)]", "[]"},
		{"list set", "#set($l = [1, 2, 3])#set($r = $l.set($wide, 9))$l", "[1, 2, 3]"},
		{"list remove by index", "#set($l = [1, 2, 3])[$!l.remove($huge)]$l", "[][1, 2, 3]"},
		{"index write", "#set($l = [1, 2, 3])#set($l[$wide] = 9)$l", "[1, 2, 3]"},
		{"substring NaN", "[$!s.substring($nan)]", "[]"},
		{"substring infinity", "[$!s.substring($inf)]", "[]"},
		{"charAt huge", "[$!s.charAt($big)]", "[]"},
		{"in range still works", "$s.substring(1, 3)$s.charAt(0)", "elh"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := render(t, tc.src, vars); got != tc.want {
				t.Fatalf("render(%q) = %q, want %q", tc.src, got, tc.want)
			}
		})
	}
}
