package azure

import (
	"reflect"
	"testing"
)

// TestMissingArrayElementsRecursesPerElement is the core capture-side guarantee:
// an unmodeled sub-field on an array element (whose sibling fields the handler
// models) is captured, aligned to the element's index, while a fully-modeled
// element contributes nothing.
func TestMissingArrayElementsRecursesPerElement(t *testing.T) {
	req := []any{
		map[string]any{"lun": float64(0), "caching": "ReadOnly", "writeAcceleratorEnabled": true},
		map[string]any{"lun": float64(1), "caching": "None"},
	}
	resp := []any{
		map[string]any{"lun": float64(0), "caching": "ReadOnly"},
		map[string]any{"lun": float64(1), "caching": "None"},
	}

	got := missingArrayElements(req, resp)

	want := []any{
		map[string]any{"writeAcceleratorEnabled": true},
		nil,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("missingArrayElements = %#v, want %#v", got, want)
	}
}

// TestMissingArrayElementsNoUnmodeled returns nil when every element is fully
// modeled, so the overlay records nothing for the array (no regression: a
// modeled array is left untouched).
func TestMissingArrayElementsNoUnmodeled(t *testing.T) {
	req := []any{map[string]any{"lun": float64(0), "caching": "ReadOnly"}}
	resp := []any{map[string]any{"lun": float64(0), "caching": "ReadOnly"}}

	if got := missingArrayElements(req, resp); got != nil {
		t.Fatalf("missingArrayElements = %#v, want nil", got)
	}
}

// TestMissingArrayElementsLengthMismatch confirms neither a longer nor a shorter
// request panics or describes more elements than the response holds. The
// response count is authoritative: a request longer than the response yields no
// phantom elements, and a request shorter simply leaves the response tail
// unmatched.
func TestMissingArrayElementsLengthMismatch(t *testing.T) {
	// Request LONGER than response: only the response-length prefix is diffed.
	reqLong := []any{
		map[string]any{"lun": float64(0), "extra": "keep0"},
		map[string]any{"lun": float64(1), "extra": "keep1"},
		map[string]any{"lun": float64(2), "extra": "phantom"},
	}
	respShort := []any{
		map[string]any{"lun": float64(0)},
	}

	got := missingArrayElements(reqLong, respShort)
	if len(got) != 1 {
		t.Fatalf("request longer than response: got len %d, want 1 (no phantom elements)", len(got))
	}

	if !reflect.DeepEqual(got[0], map[string]any{"extra": "keep0"}) {
		t.Fatalf("request longer than response: got[0] = %#v, want {extra:keep0}", got[0])
	}

	// Request SHORTER than response: only the request-length prefix is diffed;
	// the response tail is left unmatched (nil-safe, no index out of range).
	reqShort := []any{map[string]any{"lun": float64(0), "extra": "keep0"}}
	respLong := []any{
		map[string]any{"lun": float64(0)},
		map[string]any{"lun": float64(1)},
	}

	got = missingArrayElements(reqShort, respLong)
	if len(got) != 1 || !reflect.DeepEqual(got[0], map[string]any{"extra": "keep0"}) {
		t.Fatalf("request shorter than response: got = %#v, want [{extra:keep0}]", got)
	}
}

// TestMissingArrayElementsScalarsAndNil confirms scalar elements and nil/empty
// slices are no-ops at this level: a scalar element present in both is left to
// the handler (the wholly-unmodeled scalar array is captured verbatim by the
// caller's !present branch, not here), and empty inputs yield nil.
func TestMissingArrayElementsScalarsAndNil(t *testing.T) {
	if got := missingArrayElements([]any{"a", "b"}, []any{"a", "b"}); got != nil {
		t.Fatalf("scalar arrays: got %#v, want nil", got)
	}

	if got := missingArrayElements(nil, nil); got != nil {
		t.Fatalf("nil arrays: got %#v, want nil", got)
	}

	if got := missingArrayElements([]any{}, []any{map[string]any{"x": 1}}); got != nil {
		t.Fatalf("empty request: got %#v, want nil", got)
	}
}

// TestMergeArrayElementsPreservesResponseShape is the apply-side guarantee: the
// response array's length, order and modeled fields are preserved exactly, only
// the per-element unmodeled sub-fields are added back, and a modeled field is
// never overwritten.
func TestMergeArrayElementsPreservesResponseShape(t *testing.T) {
	resp := []any{
		map[string]any{"lun": float64(0), "diskSizeGB": float64(32)},
		map[string]any{"lun": float64(1), "diskSizeGB": float64(64)},
	}
	unmodeled := []any{
		// carries writeAcceleratorEnabled AND a modeled key (lun) that must NOT win.
		map[string]any{"writeAcceleratorEnabled": true, "lun": float64(99)},
		nil,
	}

	got := mergeArrayElements(resp, unmodeled)

	if len(got) != 2 {
		t.Fatalf("merge changed element count: got %d, want 2", len(got))
	}

	el0, _ := got[0].(map[string]any)
	if el0["lun"] != float64(0) {
		t.Errorf("merge overwrote modeled lun: got %v, want 0", el0["lun"])
	}

	if el0["diskSizeGB"] != float64(32) {
		t.Errorf("merge lost modeled diskSizeGB: got %v", el0["diskSizeGB"])
	}

	if el0["writeAcceleratorEnabled"] != true {
		t.Errorf("merge dropped unmodeled writeAcceleratorEnabled: got %v", el0["writeAcceleratorEnabled"])
	}

	if !reflect.DeepEqual(got[1], resp[1]) {
		t.Errorf("merge disturbed an element with no overlay: got %#v, want %#v", got[1], resp[1])
	}
}

// TestMergeArrayElementsNeverInjectsPhantom confirms an unmodeled slice longer
// than the response never appends elements the response does not have (the
// handler owns element count) and never indexes past either slice.
func TestMergeArrayElementsNeverInjectsPhantom(t *testing.T) {
	resp := []any{map[string]any{"lun": float64(0)}}
	unmodeled := []any{
		map[string]any{"extra": "keep0"},
		map[string]any{"extra": "phantom"},
	}

	got := mergeArrayElements(resp, unmodeled)
	if len(got) != 1 {
		t.Fatalf("merge injected phantom elements: got len %d, want 1", len(got))
	}

	el0, _ := got[0].(map[string]any)
	if el0["extra"] != "keep0" {
		t.Errorf("merge dropped the first element's unmodeled field: got %v", el0["extra"])
	}

	// A response longer than the overlay leaves the tail untouched.
	resp2 := []any{map[string]any{"lun": float64(0)}, map[string]any{"lun": float64(1)}}
	got2 := mergeArrayElements(resp2, []any{nil})

	if !reflect.DeepEqual(got2, resp2) {
		t.Errorf("overlay shorter than response disturbed the tail: got %#v, want %#v", got2, resp2)
	}
}

// TestMissingPropertiesArrayEndToEnd exercises the whole capture+merge cycle
// through the public helpers exactly as the middleware chains them: an unmodeled
// element sub-field nested under a modeled array (storageProfile.dataDisks)
// survives, while the modeled fields stay authoritative and no element is added.
func TestMissingPropertiesArrayEndToEnd(t *testing.T) {
	req := map[string]any{
		"storageProfile": map[string]any{
			"dataDisks": []any{
				map[string]any{"lun": float64(0), "diskSizeGB": float64(32), "writeAcceleratorEnabled": true},
			},
		},
	}
	resp := map[string]any{
		"storageProfile": map[string]any{
			"dataDisks": []any{
				map[string]any{"lun": float64(0), "diskSizeGB": float64(32), "caching": "ReadOnly"},
			},
		},
	}

	unmodeled := missingProperties(req, resp)

	merged := mergeProperties(resp, unmodeled)

	sp, _ := merged["storageProfile"].(map[string]any)
	disks, _ := sp["dataDisks"].([]any)

	if len(disks) != 1 {
		t.Fatalf("end-to-end changed disk count: got %d, want 1", len(disks))
	}

	d0, _ := disks[0].(map[string]any)
	if d0["writeAcceleratorEnabled"] != true {
		t.Errorf("end-to-end dropped unmodeled writeAcceleratorEnabled: got %v", d0["writeAcceleratorEnabled"])
	}

	if d0["lun"] != float64(0) || d0["diskSizeGB"] != float64(32) || d0["caching"] != "ReadOnly" {
		t.Errorf("end-to-end disturbed modeled dataDisk fields: %#v", d0)
	}
}
