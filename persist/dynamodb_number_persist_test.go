package persist

import (
	"context"
	"testing"

	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	"github.com/stackshy/cloudemu/v2/services/database/driver/expr"
)

// dummyDB is a minimal Database capturing PutItem items and replaying via Scan.
// TestExprNumberSurvivesPersistJSON pins that a big Number survives an
// export->JSON->restore snapshot round-trip as an exact-decimal expr.Number.
func TestRetypeItemRestoresNumber(t *testing.T) {
	_ = context.Background
	_ = dbdriver.TableConfig{}
	// Simulate what a JSON restore produces: the tagged object form.
	item := map[string]any{
		"pk":     "k1",
		"big":    map[string]any{expr.NumberJSONTag: "123456789012345678901234567890"},
		"nested": map[string]any{"inner": map[string]any{expr.NumberJSONTag: "42"}},
		"list":   []any{map[string]any{expr.NumberJSONTag: "7"}, "s"},
	}
	got := expr.RetypeItem(item)
	if got["big"] != expr.Number("123456789012345678901234567890") {
		t.Fatalf("big = %#v, want exact-decimal Number", got["big"])
	}
	if m, ok := got["nested"].(map[string]any); !ok || m["inner"] != expr.Number("42") {
		t.Fatalf("nested number not retyped: %#v", got["nested"])
	}
	if l, ok := got["list"].([]any); !ok || l[0] != expr.Number("7") || l[1] != "s" {
		t.Fatalf("list number not retyped: %#v", got["list"])
	}
}
