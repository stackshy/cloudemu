package metastore

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	msdriver "github.com/stackshy/cloudemu/v2/services/metastore/driver"
)

func newMock(t *testing.T) *Mock {
	t.Helper()

	return New(config.NewOptions(config.WithProjectID("p")))
}

func rawFields(kv map[string]string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range kv {
		out[k] = json.RawMessage(`"` + v + `"`)
	}

	return out
}

func TestServiceCRUD(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	cfg := &msdriver.Config{
		Project: "p", Location: "us-central1", ID: "svc",
		Fields: rawFields(map[string]string{"tier": "DEVELOPER", "network": "projects/p/global/networks/default"}),
	}

	res, op, err := m.CreateService(ctx, cfg)
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	if !op.Done || op.Type != "create" {
		t.Fatalf("op = %+v", op)
	}

	if res.CreateTime.IsZero() || res.UpdateTime.IsZero() {
		t.Fatalf("computed timestamps missing: %+v", res)
	}

	got, err := m.GetService(ctx, "p", "us-central1", "svc")
	if err != nil {
		t.Fatalf("GetService: %v", err)
	}

	if string(got.Fields["tier"]) != `"DEVELOPER"` {
		t.Fatalf("tier not stored: %s", got.Fields["tier"])
	}

	// Duplicate create -> AlreadyExists.
	if _, _, err := m.CreateService(ctx, cfg); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate create err = %v, want AlreadyExists", err)
	}

	// Deep-clone isolation: mutating the returned Fields must not affect the store.
	got.Fields["tier"] = json.RawMessage(`"tampered"`)

	again, err := m.GetService(ctx, "p", "us-central1", "svc")
	if err != nil {
		t.Fatalf("GetService: %v", err)
	}

	if string(again.Fields["tier"]) != `"DEVELOPER"` {
		t.Fatalf("store aliased by returned value: %s", again.Fields["tier"])
	}
}

func TestServiceListScoping(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	for _, id := range []string{"a", "b"} {
		if _, _, err := m.CreateService(ctx, &msdriver.Config{
			Project: "p", Location: "us-central1", ID: id,
		}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	// A service in a different region must not leak into the scope.
	if _, _, err := m.CreateService(ctx, &msdriver.Config{
		Project: "p", Location: "europe-west1", ID: "c",
	}); err != nil {
		t.Fatalf("create c: %v", err)
	}

	list, err := m.ListServices(ctx, "p", "us-central1")
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("list scoped to region should be 2, got %d", len(list))
	}
}

func TestServicePatchMasked(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, _, err := m.CreateService(ctx, &msdriver.Config{
		Project: "p", Location: "us-central1", ID: "svc",
		Fields: rawFields(map[string]string{"tier": "DEVELOPER", "network": "default"}),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	_, op, err := m.PatchService(ctx, &msdriver.Config{
		Project: "p", Location: "us-central1", ID: "svc",
		Fields: map[string]json.RawMessage{"labels": json.RawMessage(`{"env":"prod"}`)},
	}, []string{"labels"})
	if err != nil {
		t.Fatalf("PatchService: %v", err)
	}

	if !op.Done || op.Type != "update" {
		t.Fatalf("op = %+v", op)
	}

	got, err := m.GetService(ctx, "p", "us-central1", "svc")
	if err != nil {
		t.Fatalf("GetService: %v", err)
	}

	if string(got.Fields["labels"]) != `{"env":"prod"}` {
		t.Fatalf("labels not patched: %s", got.Fields["labels"])
	}

	// Unmasked field survives.
	if string(got.Fields["network"]) != `"default"` {
		t.Fatalf("unmasked network mutated: %s", got.Fields["network"])
	}
}

func TestServiceDeleteAndNotFound(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, err := m.GetService(ctx, "p", "us-central1", "ghost"); !cerrors.IsNotFound(err) {
		t.Fatalf("GetService missing err = %v, want NotFound", err)
	}

	if _, _, err := m.CreateService(ctx, &msdriver.Config{
		Project: "p", Location: "us-central1", ID: "svc",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	op, err := m.DeleteService(ctx, "p", "us-central1", "svc")
	if err != nil {
		t.Fatalf("DeleteService: %v", err)
	}

	if !op.Done || op.Type != "delete" {
		t.Fatalf("op = %+v", op)
	}

	if _, err := m.GetService(ctx, "p", "us-central1", "svc"); !cerrors.IsNotFound(err) {
		t.Fatalf("expected NotFound after delete, got %v", err)
	}

	if _, err := m.DeleteService(ctx, "p", "us-central1", "svc"); !cerrors.IsNotFound(err) {
		t.Fatalf("delete missing err = %v, want NotFound", err)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, _, err := m.CreateService(ctx, &msdriver.Config{
		Project: "p", Location: "us-central1", ID: "svc",
		Fields: rawFields(map[string]string{"tier": "ENTERPRISE"}),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	blob, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restored := newMock(t)
	if err := restored.Restore(ctx, blob); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	got, err := restored.GetService(ctx, "p", "us-central1", "svc")
	if err != nil {
		t.Fatalf("GetService after restore: %v", err)
	}

	if string(got.Fields["tier"]) != `"ENTERPRISE"` {
		t.Fatalf("restored service missing tier: %s", got.Fields["tier"])
	}
}
