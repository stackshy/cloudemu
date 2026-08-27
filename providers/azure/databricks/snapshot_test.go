package databricks

import (
	"bytes"
	"context"
	"testing"
)

// TestSnapshotRoundTripDatabricks proves a snapshot/restore cycle reinstates the
// mock's state under the original identities: a workspace created before the
// snapshot is readable from a fresh mock after restore, and re-snapshotting the
// restored mock yields byte-identical JSON (every store round-trips).
func TestSnapshotRoundTripDatabricks(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	ws, err := src.CreateWorkspace(ctx, validConfig())
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	got, err := dst.GetWorkspace(ctx, "rg-1", "ws-1")
	if err != nil {
		t.Fatalf("get restored workspace: %v", err)
	}

	if got.ID != ws.ID {
		t.Fatalf("restored workspace ID = %q, want %q", got.ID, ws.ID)
	}

	reSnap, err := dst.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("re-snapshot: %v", err)
	}

	if !bytes.Equal(raw, reSnap) {
		t.Fatalf("re-snapshot differs from original snapshot; a store did not round-trip")
	}
}
