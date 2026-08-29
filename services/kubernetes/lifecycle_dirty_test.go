package kubernetes_test

import (
	"net/http"
	"testing"
	"time"
)

// TestTickReportsAdvanceForDirtySeam covers the signal the serve progression
// ticker relies on to mark persistence state dirty: Tick returns true ONLY when a
// staged Pod actually advanced a stage this tick, and false on an idle tick (no
// Pod due). Without a truthful signal the ticker would either never persist a
// staged advance (a background mutation that bypasses the HTTP dirty seam) or
// re-save on every idle tick.
func TestTickReportsAdvanceForDirtySeam(t *testing.T) {
	f, done := newProgressionFixture(t)
	defer done()

	// Nothing staged yet → an idle tick reports no change.
	if f.state.Tick() {
		t.Fatal("Tick() = true on an idle cluster; want false")
	}

	// A bare Pod starts Pending under progression.
	podBody := mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "staged"},
		"spec":     map[string]any{"containers": []any{map[string]any{"name": "c", "image": "busybox"}}},
	})
	doAccept(t, http.MethodPost, f.base+"/api/v1/namespaces/default/pods", "", podBody).Body.Close()

	// Before its transition is due, a tick advances nothing.
	if f.state.Tick() {
		t.Fatal("Tick() = true before the Pod's transition was due; want false")
	}

	// Once due, the tick advances the Pod (Pending -> ContainerCreating) and must
	// report the change so the ticker marks state dirty.
	f.clock.Advance(2 * time.Second)

	if !f.state.Tick() {
		t.Fatal("Tick() = false after a staged Pod advanced; want true (dirty seam would miss the change)")
	}
}
