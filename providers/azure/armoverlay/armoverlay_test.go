package armoverlay

import (
	"context"
	"testing"
)

func TestOverlayCaptureLookupEvict(t *testing.T) {
	m := New(nil)

	const (
		rg1   = "/subscriptions/s/resourceGroups/rg1"
		rg10  = "/subscriptions/s/resourceGroups/rg10"
		vnet1 = rg1 + "/providers/Microsoft.Network/virtualNetworks/v1"
	)

	m.Capture(vnet1, map[string]any{"p": map[string]any{"k": "v"}})
	m.Capture(rg10+"/providers/Microsoft.Network/virtualNetworks/v2", map[string]any{"p": "x"})

	got := m.Lookup(vnet1)
	got["p"].(map[string]any)["k"] = "mutated"

	if v := m.Lookup(vnet1)["p"].(map[string]any)["k"]; v != "v" {
		t.Fatalf("stored value = %v, want v (Lookup must return a copy)", v)
	}

	m.EvictTree("/subscriptions/s/resourcegroups/RG1/")

	if m.Lookup(vnet1) != nil {
		t.Error("EvictTree left an entry under the evicted group")
	}

	if m.Lookup(rg10+"/providers/Microsoft.Network/virtualNetworks/v2") == nil {
		t.Error("EvictTree of rg1 removed an entry under rg10")
	}

	m.Capture(rg10, map[string]any{"a": 1.0})
	m.Capture(rg10, nil)

	if m.Lookup(rg10) != nil {
		t.Error("an empty Capture did not clear the entry")
	}
}

func TestOverlaySnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := New(nil)
	src.Capture("/subscriptions/s/resourceGroups/rg/x", map[string]any{"policy": "Disabled"})

	data, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatal(err)
	}

	dst := New(nil)
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatal(err)
	}

	if got := dst.Lookup("/subscriptions/s/resourceGroups/rg/x"); got["policy"] != "Disabled" {
		t.Fatalf("restored entry = %v", got)
	}
}
