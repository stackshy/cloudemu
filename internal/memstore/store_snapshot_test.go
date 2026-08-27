package memstore

import (
	"testing"
)

type snapValue struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// TestStoreSnapshotRoundTrip proves a store's Snapshot/LoadSnapshot round-trip
// preserves both keys (identity) and values.
func TestStoreSnapshotRoundTrip(t *testing.T) {
	src := New[snapValue]()
	src.Set("id-1", snapValue{Name: "alpha", Count: 3})
	src.Set("id-2", snapValue{Name: "beta", Count: 7})

	data, err := src.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := New[snapValue]()
	if err := dst.LoadSnapshot(data); err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}

	if dst.Len() != 2 {
		t.Fatalf("restored len = %d, want 2", dst.Len())
	}

	for _, key := range []string{"id-1", "id-2"} {
		want, _ := src.Get(key)
		got, ok := dst.Get(key)
		if !ok {
			t.Fatalf("restored store missing key %q", key)
		}

		if got != want {
			t.Fatalf("restored[%q] = %+v, want %+v", key, got, want)
		}
	}
}

// TestStoreLoadSnapshotMerges verifies LoadSnapshot restores under the original
// keys onto a store that already holds unrelated entries.
func TestStoreLoadSnapshotMerges(t *testing.T) {
	src := New[snapValue]()
	src.Set("id-1", snapValue{Name: "alpha"})

	data, err := src.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := New[snapValue]()
	dst.Set("id-existing", snapValue{Name: "keep"})
	if err := dst.LoadSnapshot(data); err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}

	if !dst.Has("id-existing") || !dst.Has("id-1") {
		t.Fatalf("expected both existing and restored keys, got keys %v", dst.Keys())
	}
}
