package gcprest

import "testing"

// TestOperationRegistryCreateDeleteBounded checks a create and delete loop
// keeps the scope's name queue bounded and drops a bucket that empties.
func TestOperationRegistryCreateDeleteBounded(t *testing.T) {
	reg := NewOperationRegistry()
	keep := reg.RecordDone("http://h", "p", ScopeZones, "z", "disks", "keep", "insert")

	for range 10000 {
		op := reg.RecordDone("http://h", "p", ScopeZones, "z", "disks", "d", "insert")
		if !reg.Delete("p", ScopeZones, "z", op.Name) {
			t.Fatal("delete failed")
		}
	}

	b := reg.buckets[opKey("p", ScopeZones, "z", "")]
	if n := len(b.names) - b.head; n > 2*b.live+16 || b.live != 1 {
		t.Errorf("queue holds %d names for %d live ops, want bounded", n, b.live)
	}

	if _, ok := reg.Get("p", ScopeZones, "z", keep.Name); !ok {
		t.Error("surviving op lost by compaction")
	}

	reg.Delete("p", ScopeZones, "z", keep.Name)

	if len(reg.buckets) != 0 {
		t.Errorf("empty bucket kept: %d buckets", len(reg.buckets))
	}
}
