package vpcaccess

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	vpcdriver "github.com/stackshy/cloudemu/v2/services/vpcaccess/driver"
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

func TestConnectorCRUD(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	cfg := &vpcdriver.Config{
		Project: "p", Location: "us-central1", ID: "conn",
		Fields: rawFields(map[string]string{"network": "default", "ipCidrRange": "10.8.0.0/28"}),
	}

	res, op, err := m.CreateConnector(ctx, cfg)
	if err != nil {
		t.Fatalf("CreateConnector: %v", err)
	}

	if !op.Done || op.Type != "create" {
		t.Fatalf("op = %+v", op)
	}

	if res.CreateTime.IsZero() || res.UpdateTime.IsZero() {
		t.Fatalf("computed timestamps missing: %+v", res)
	}

	got, err := m.GetConnector(ctx, "p", "us-central1", "conn")
	if err != nil {
		t.Fatalf("GetConnector: %v", err)
	}

	if string(got.Fields["network"]) != `"default"` {
		t.Fatalf("network not stored: %s", got.Fields["network"])
	}

	// Duplicate create -> AlreadyExists.
	if _, _, err := m.CreateConnector(ctx, cfg); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate create err = %v, want AlreadyExists", err)
	}

	// Deep-clone isolation: mutating the returned Fields must not affect the store.
	got.Fields["network"] = json.RawMessage(`"tampered"`)

	again, err := m.GetConnector(ctx, "p", "us-central1", "conn")
	if err != nil {
		t.Fatalf("GetConnector: %v", err)
	}

	if string(again.Fields["network"]) != `"default"` {
		t.Fatalf("store aliased by returned value: %s", again.Fields["network"])
	}
}

func TestConnectorListScoping(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	for _, id := range []string{"a", "b"} {
		if _, _, err := m.CreateConnector(ctx, &vpcdriver.Config{
			Project: "p", Location: "us-central1", ID: id,
		}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	// A connector in a different region must not leak into the scope.
	if _, _, err := m.CreateConnector(ctx, &vpcdriver.Config{
		Project: "p", Location: "europe-west1", ID: "c",
	}); err != nil {
		t.Fatalf("create c: %v", err)
	}

	list, err := m.ListConnectors(ctx, "p", "us-central1")
	if err != nil {
		t.Fatalf("ListConnectors: %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("list scoped to region should be 2, got %d", len(list))
	}
}

func TestConnectorPatchMasked(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, _, err := m.CreateConnector(ctx, &vpcdriver.Config{
		Project: "p", Location: "us-central1", ID: "conn",
		Fields: rawFields(map[string]string{"network": "default", "machineType": "e2-micro"}),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	_, op, err := m.PatchConnector(ctx, &vpcdriver.Config{
		Project: "p", Location: "us-central1", ID: "conn",
		Fields: map[string]json.RawMessage{"maxInstances": json.RawMessage(`5`)},
	}, []string{"maxInstances"})
	if err != nil {
		t.Fatalf("PatchConnector: %v", err)
	}

	if !op.Done || op.Type != "update" {
		t.Fatalf("op = %+v", op)
	}

	got, err := m.GetConnector(ctx, "p", "us-central1", "conn")
	if err != nil {
		t.Fatalf("GetConnector: %v", err)
	}

	if string(got.Fields["maxInstances"]) != `5` {
		t.Fatalf("maxInstances not patched: %s", got.Fields["maxInstances"])
	}

	// Unmasked field survives.
	if string(got.Fields["network"]) != `"default"` {
		t.Fatalf("unmasked network mutated: %s", got.Fields["network"])
	}
}

func TestConnectorDeleteAndNotFound(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, err := m.GetConnector(ctx, "p", "us-central1", "ghost"); !cerrors.IsNotFound(err) {
		t.Fatalf("GetConnector missing err = %v, want NotFound", err)
	}

	if _, _, err := m.CreateConnector(ctx, &vpcdriver.Config{
		Project: "p", Location: "us-central1", ID: "conn",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	op, err := m.DeleteConnector(ctx, "p", "us-central1", "conn")
	if err != nil {
		t.Fatalf("DeleteConnector: %v", err)
	}

	if !op.Done || op.Type != "delete" {
		t.Fatalf("op = %+v", op)
	}

	if _, err := m.GetConnector(ctx, "p", "us-central1", "conn"); !cerrors.IsNotFound(err) {
		t.Fatalf("expected NotFound after delete, got %v", err)
	}

	if _, err := m.DeleteConnector(ctx, "p", "us-central1", "conn"); !cerrors.IsNotFound(err) {
		t.Fatalf("delete missing err = %v, want NotFound", err)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, _, err := m.CreateConnector(ctx, &vpcdriver.Config{
		Project: "p", Location: "us-central1", ID: "conn",
		Fields: rawFields(map[string]string{"network": "default"}),
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

	got, err := restored.GetConnector(ctx, "p", "us-central1", "conn")
	if err != nil {
		t.Fatalf("GetConnector after restore: %v", err)
	}

	if string(got.Fields["network"]) != `"default"` {
		t.Fatalf("restored connector missing network: %s", got.Fields["network"])
	}
}
