package backup

import (
	"math"
	"net/http/httptest"
	"strconv"
	"testing"
)

// TestAtoiDefault covers the maxResults query-param parsing: a well-formed
// count round-trips, and anything empty, invalid, negative, or too large to
// fit an int32 (go/incorrect-integer-conversion) is rejected to 0 rather than
// wrapping/truncating on the int32(n) conversion.
func TestAtoiDefault(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int32
	}{
		{"empty", "", 0},
		{"zero", "0", 0},
		{"normal", "50", 50},
		{"negative", "-1", 0},
		{"invalid", "not-a-number", 0},
		{"maxInt32", strconv.Itoa(math.MaxInt32), math.MaxInt32},
		{"overflowsInt32", strconv.FormatInt(math.MaxInt32+1, 10), 0},
		{"wayTooLarge", "999999999999999999999", 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := atoiDefault(tc.in); got != tc.want {
				t.Fatalf("atoiDefault(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestPageFromQuery confirms an out-of-range maxResults on the wire is
// clamped away rather than propagated as a garbage negative page size.
func TestPageFromQuery(t *testing.T) {
	overflow := strconv.FormatInt(math.MaxInt32+1, 10)
	r := httptest.NewRequest("GET", "/backup-vaults?maxResults="+overflow+"&nextToken=abc", nil)

	page := pageFromQuery(r)
	if page.MaxResults != 0 {
		t.Fatalf("MaxResults = %d, want 0 for an out-of-int32-range maxResults", page.MaxResults)
	}

	if page.NextToken != "abc" {
		t.Fatalf("NextToken = %q, want %q", page.NextToken, "abc")
	}
}
