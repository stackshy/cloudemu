package gkebackup

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	gkbdriver "github.com/stackshy/cloudemu/v2/services/gkebackup/driver"
)

func newMock() *Mock {
	return New(config.NewOptions(config.WithProjectID("p")))
}

func cfg(id string, fields map[string]json.RawMessage) *gkbdriver.Config {
	return &gkbdriver.Config{Project: "p", Location: "us-central1", ID: id, Fields: fields}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestBackupPlanComputedIdentity checks uid is deterministic from the resource
// name, state derives from deactivated, and etag rotates on a mutating patch
// while uid stays stable.
func TestBackupPlanComputedIdentity(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	res, op, err := m.CreateBackupPlan(ctx, cfg("plan", map[string]json.RawMessage{
		"cluster": json.RawMessage(`"projects/p/locations/us-central1/clusters/c"`),
	}))
	requireNoError(t, err)

	if !op.Done {
		t.Fatalf("create op not done")
	}

	if res.UID == "" || res.Etag == "" {
		t.Fatalf("uid/etag not minted: %+v", res)
	}

	if res.State != stateReady {
		t.Fatalf("state = %q, want READY", res.State)
	}

	// uid is deterministic from the resource name.
	if want := deterministicUID(resourceName(backupPlansColl, "p", "us-central1", "plan")); res.UID != want {
		t.Fatalf("uid = %q, want deterministic %q", res.UID, want)
	}

	// A second mock creates the identical uid for the same name.
	res2, _, err := newMock().CreateBackupPlan(ctx, cfg("plan", nil))
	requireNoError(t, err)

	if res2.UID != res.UID {
		t.Fatalf("uid not deterministic across instances: %q vs %q", res.UID, res2.UID)
	}

	patched, _, err := m.PatchBackupPlan(ctx, cfg("plan", map[string]json.RawMessage{
		"description": json.RawMessage(`"changed"`),
	}), []string{"description"})
	requireNoError(t, err)

	if patched.UID != res.UID {
		t.Fatalf("uid must be stable across patch")
	}

	if patched.Etag == res.Etag {
		t.Fatalf("etag must rotate on mutation")
	}
}

// TestBackupPlanDeactivatedState checks the state derivation from deactivated.
func TestBackupPlanDeactivatedState(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	res, _, err := m.CreateBackupPlan(ctx, cfg("off", map[string]json.RawMessage{
		"deactivated": json.RawMessage(`true`),
	}))
	requireNoError(t, err)

	if res.State != stateDeactivated {
		t.Fatalf("state = %q, want DEACTIVATED", res.State)
	}

	// Restore plans have no deactivated flag: always READY.
	rp, _, err := m.CreateRestorePlan(ctx, cfg("r", map[string]json.RawMessage{
		"deactivated": json.RawMessage(`true`),
	}))
	requireNoError(t, err)

	if rp.State != stateReady {
		t.Fatalf("restore plan state = %q, want READY", rp.State)
	}
}

// TestDuplicateAndNotFound covers 409 and 404 semantics.
func TestDuplicateAndNotFound(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, err := m.GetBackupPlan(ctx, "p", "us-central1", "ghost"); err == nil {
		t.Fatalf("expected not found")
	}

	if _, _, err := m.CreateBackupPlan(ctx, cfg("dup", nil)); err != nil {
		t.Fatalf("first create: %v", err)
	}

	if _, _, err := m.CreateBackupPlan(ctx, cfg("dup", nil)); err == nil {
		t.Fatalf("expected already-exists on duplicate")
	}
}

// TestSnapshotRestore checks the mock round-trips its full state.
func TestSnapshotRestore(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, _, err := m.CreateBackupPlan(ctx, cfg("plan", map[string]json.RawMessage{
		"cluster": json.RawMessage(`"c"`),
	})); err != nil {
		t.Fatalf("create backup plan: %v", err)
	}

	if _, _, err := m.CreateRestorePlan(ctx, cfg("restore", nil)); err != nil {
		t.Fatalf("create restore plan: %v", err)
	}

	blob, err := m.Snapshot(ctx, false)
	requireNoError(t, err)

	restored := newMock()
	requireNoError(t, restored.Restore(ctx, blob))

	got, err := restored.GetBackupPlan(ctx, "p", "us-central1", "plan")
	requireNoError(t, err)

	if got.State != stateReady || string(got.Fields["cluster"]) != `"c"` {
		t.Fatalf("restored backup plan wrong: %+v", got)
	}

	if _, err := restored.GetRestorePlan(ctx, "p", "us-central1", "restore"); err != nil {
		t.Fatalf("restored restore plan missing: %v", err)
	}
}
