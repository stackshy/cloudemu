package expr

import (
	"testing"
)

// TestRetypeItemReconstructsNumbers pins that RetypeItem rebuilds every tagged
// Number, recursing through nested maps and lists, and leaves other values
// unchanged.
func TestRetypeItemReconstructsNumbers(t *testing.T) {
	item := map[string]any{
		"pk":     "k1",
		"big":    map[string]any{NumberJSONTag: "123456789012345678901234567890"},
		"nested": map[string]any{"inner": map[string]any{NumberJSONTag: "42"}},
		"list":   []any{map[string]any{NumberJSONTag: "7"}, "s"},
	}

	got := RetypeItem(item)

	if got["pk"] != "k1" {
		t.Fatalf("pk = %#v, want unchanged string", got["pk"])
	}

	if got["big"] != Number("123456789012345678901234567890") {
		t.Fatalf("big = %#v, want exact-decimal Number", got["big"])
	}

	m, ok := got["nested"].(map[string]any)
	if !ok || m["inner"] != Number("42") {
		t.Fatalf("nested number not retyped: %#v", got["nested"])
	}

	l, ok := got["list"].([]any)
	if !ok || l[0] != Number("7") || l[1] != "s" {
		t.Fatalf("list number not retyped: %#v", got["list"])
	}
}
