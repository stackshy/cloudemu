package gcprest_test

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// TestOperationRegistryRetention checks a scope keeps only the newest
// MaxOperationsPerScope operations, evicting oldest first, without touching
// another scope, and that a deleted operation frees its slot.
func TestOperationRegistryRetention(t *testing.T) {
	const extra = 25

	reg := gcprest.NewOperationRegistry()
	zone := func() gcprest.Operation {
		return reg.RecordDone("http://h", "p", gcprest.ScopeZones, "z", "disks", "d", "insert")
	}

	other := reg.RecordDone("http://h", "p", gcprest.ScopeGlobal, "", "networks", "n", "insert")

	var minted []string

	for range gcprest.MaxOperationsPerScope + extra {
		minted = append(minted, zone().Name)
	}

	got := reg.List("p", gcprest.ScopeZones, "z")
	if len(got) != gcprest.MaxOperationsPerScope {
		t.Fatalf("retained %d ops, want %d", len(got), gcprest.MaxOperationsPerScope)
	}

	if got[0].Name != minted[extra] || got[len(got)-1].Name != minted[len(minted)-1] {
		t.Errorf("retained %s..%s, want the newest %s..%s", got[0].Name, got[len(got)-1].Name,
			minted[extra], minted[len(minted)-1])
	}

	for _, name := range minted[:extra] {
		if _, ok := reg.Get("p", gcprest.ScopeZones, "z", name); ok {
			t.Fatalf("evicted op %s still readable", name)
		}
	}

	if _, ok := reg.Get("p", gcprest.ScopeGlobal, "", other.Name); !ok {
		t.Error("op in another scope was evicted")
	}

	if !reg.Delete("p", gcprest.ScopeZones, "z", minted[len(minted)-1]) {
		t.Fatal("delete newest op failed")
	}

	next := zone().Name
	if _, ok := reg.Get("p", gcprest.ScopeZones, "z", minted[extra]); !ok {
		t.Error("insert after a delete evicted an op although the scope was under the cap")
	}

	if n := len(reg.List("p", gcprest.ScopeZones, "z")); n != gcprest.MaxOperationsPerScope {
		t.Errorf("after delete+insert retained %d, want %d (newest %s)", n, gcprest.MaxOperationsPerScope, next)
	}
}
