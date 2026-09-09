package securesourcemanager

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	ssmdriver "github.com/stackshy/cloudemu/v2/services/securesourcemanager/driver"
)

func newMock(t *testing.T) *Mock {
	t.Helper()

	return New(config.NewOptions(config.WithProjectID("p")))
}

func raw(kv map[string]string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range kv {
		out[k] = json.RawMessage(`"` + v + `"`)
	}

	return out
}

func TestInstanceCRUD(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	cfg := &ssmdriver.Config{
		Project: "p", Location: "us-central1", ID: "inst",
		Fields: raw(map[string]string{"kmsKey": "projects/p/locations/us-central1/keyRings/r/cryptoKeys/k"}),
	}

	res, op, err := m.CreateInstance(ctx, cfg)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if !op.Done || op.Type != "create" {
		t.Fatalf("op = %+v", op)
	}

	if res.CreateTime.IsZero() || res.UpdateTime.IsZero() {
		t.Fatalf("computed timestamps missing: %+v", res)
	}

	got, err := m.GetInstance(ctx, "p", "us-central1", "inst")
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}

	if string(got.Fields["kmsKey"]) == "" {
		t.Fatalf("kmsKey not stored")
	}

	// Duplicate create -> AlreadyExists.
	if _, _, err := m.CreateInstance(ctx, cfg); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate create err = %v, want AlreadyExists", err)
	}

	// Deep-clone isolation: mutating a returned Fields must not affect the store.
	got.Fields["kmsKey"] = json.RawMessage(`"tampered"`)

	again, err := m.GetInstance(ctx, "p", "us-central1", "inst")
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}

	if string(again.Fields["kmsKey"]) == `"tampered"` {
		t.Fatalf("store aliased by returned value")
	}

	op, err = m.DeleteInstance(ctx, "p", "us-central1", "inst")
	if err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}

	if !op.Done || op.Type != "delete" {
		t.Fatalf("delete op = %+v", op)
	}

	if _, err := m.GetInstance(ctx, "p", "us-central1", "inst"); !cerrors.IsNotFound(err) {
		t.Fatalf("expected NotFound after delete, got %v", err)
	}
}

func TestRepositoryCRUDAndPatch(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	cfg := &ssmdriver.Config{
		Project: "p", Location: "us-central1", ID: "repo",
		Fields: raw(map[string]string{
			"instance":    "projects/p/locations/us-central1/instances/inst",
			"description": "first",
		}),
	}

	if _, _, err := m.CreateRepository(ctx, cfg); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	_, op, err := m.PatchRepository(ctx, &ssmdriver.Config{
		Project: "p", Location: "us-central1", ID: "repo",
		Fields: raw(map[string]string{"description": "updated"}),
	}, []string{"description"})
	if err != nil {
		t.Fatalf("PatchRepository: %v", err)
	}

	if !op.Done || op.Type != "update" {
		t.Fatalf("patch op = %+v", op)
	}

	got, err := m.GetRepository(ctx, "p", "us-central1", "repo")
	if err != nil {
		t.Fatalf("GetRepository: %v", err)
	}

	if string(got.Fields["description"]) != `"updated"` {
		t.Fatalf("description not patched: %s", got.Fields["description"])
	}

	// Unmasked field survives the patch.
	if string(got.Fields["instance"]) != `"projects/p/locations/us-central1/instances/inst"` {
		t.Fatalf("unmasked instance mutated: %s", got.Fields["instance"])
	}
}

func TestListScoping(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	for _, id := range []string{"a", "b"} {
		if _, _, err := m.CreateInstance(ctx, &ssmdriver.Config{
			Project: "p", Location: "us-central1", ID: id,
		}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	// A different region must not leak into the scope.
	if _, _, err := m.CreateInstance(ctx, &ssmdriver.Config{
		Project: "p", Location: "europe-west1", ID: "c",
	}); err != nil {
		t.Fatalf("create c: %v", err)
	}

	list, err := m.ListInstances(ctx, "p", "us-central1")
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("list scoped to region should be 2, got %d", len(list))
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, _, err := m.CreateInstance(ctx, &ssmdriver.Config{
		Project: "p", Location: "us-central1", ID: "inst",
		Fields: raw(map[string]string{"state": "ACTIVE"}),
	}); err != nil {
		t.Fatalf("create instance: %v", err)
	}

	if _, _, err := m.CreateRepository(ctx, &ssmdriver.Config{
		Project: "p", Location: "us-central1", ID: "repo",
		Fields: raw(map[string]string{"instance": "projects/p/locations/us-central1/instances/inst"}),
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	blob, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restored := newMock(t)
	if err := restored.Restore(ctx, blob); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if _, err := restored.GetInstance(ctx, "p", "us-central1", "inst"); err != nil {
		t.Fatalf("GetInstance after restore: %v", err)
	}

	if _, err := restored.GetRepository(ctx, "p", "us-central1", "repo"); err != nil {
		t.Fatalf("GetRepository after restore: %v", err)
	}
}

func TestGetOperationSyntheticDone(t *testing.T) {
	m := newMock(t)

	op, err := m.GetOperation(context.Background(), "projects/p/locations/us-central1/operations/unknown")
	if err != nil {
		t.Fatalf("GetOperation: %v", err)
	}

	if !op.Done {
		t.Fatalf("unknown operation should report done")
	}
}
