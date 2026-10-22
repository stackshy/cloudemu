package insights

import (
	"math"
	"testing"
)

func TestClampIndex(t *testing.T) {
	tests := []struct {
		name   string
		f      float64
		max    int
		want   int
		wantOK bool
	}{
		{"nan", math.NaN(), 5, 0, false},
		{"plus inf", math.Inf(1), 5, 0, false},
		{"minus inf", math.Inf(-1), 5, 0, false},
		{"huge", 1e300, 5, 5, true},
		{"huge negative", -1e300, 5, 0, true},
		{"beyond int64", float64(math.MaxInt64) * 4, 3, 3, true},
		{"fraction truncates", 2.9, 5, 2, true},
		{"in range", 3, 5, 3, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := clampIndex(tt.f, tt.max)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("clampIndex(%v, %d) = (%d, %v), want (%d, %v)", tt.f, tt.max, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestSubstrInvalidArgs(t *testing.T) {
	tests := []struct {
		name string
		args []any
		want any
	}{
		{"nan start", []any{"abcdef", "nan"}, nil},
		{"inf start", []any{"abcdef", "inf"}, nil},
		{"minus inf start", []any{"abcdef", "-inf"}, nil},
		{"huge start", []any{"abcdef", 1e300}, nil},
		{"negative start", []any{"abcdef", -1.0}, nil},
		{"nan length", []any{"abcdef", 1.0, "nan"}, nil},
		{"inf length", []any{"abcdef", 1.0, "+Inf"}, nil},
		{"huge length", []any{"abcdef", 1.0, 1e300}, "bcdef"},
		{"negative length", []any{"abcdef", 2.0, -5.0}, ""},
		{"huge negative length", []any{"abcdef", 2.0, -1e300}, ""},
		{"start at end", []any{"abc", 3.0}, ""},
		{"normal", []any{"abcdef", 1.0, 3.0}, "bcd"},
		{"non-numeric length", []any{"abcdef", 4.0, "x"}, "ef"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := substr(tt.args); got != tt.want {
				t.Fatalf("substr(%v) = %#v, want %#v", tt.args, got, tt.want)
			}
		})
	}
}

func TestSubstrNaNQueryDoesNotPanic(t *testing.T) {
	res := run(t, `fields substr(@message, "nan") as x, substr(@message, 0, "-inf") as y`)
	if len(res.Rows) == 0 {
		t.Fatal("expected rows")
	}
}
