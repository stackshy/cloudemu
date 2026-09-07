package cloudids

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	idsdriver "github.com/stackshy/cloudemu/v2/services/cloudids/driver"
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

func TestEndpointCRUD(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	cfg := &idsdriver.Config{
		Project: "p", Location: "us-central1", ID: "ep",
		Fields: rawFields(map[string]string{"network": "default", "severity": "HIGH"}),
	}

	res, op, err := m.CreateEndpoint(ctx, cfg)
	if err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}

	if !op.Done || op.Type != "create" {
		t.Fatalf("op = %+v", op)
	}

	if res.CreateTime.IsZero() || res.UpdateTime.IsZero() {
		t.Fatalf("computed timestamps missing: %+v", res)
	}

	got, err := m.GetEndpoint(ctx, "p", "us-central1", "ep")
	if err != nil {
		t.Fatalf("GetEndpoint: %v", err)
	}

	if string(got.Fields["severity"]) != `"HIGH"` {
		t.Fatalf("severity not stored: %s", got.Fields["severity"])
	}

	// Duplicate create -> AlreadyExists.
	if _, _, err := m.CreateEndpoint(ctx, cfg); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate create err = %v, want AlreadyExists", err)
	}

	// Deep-clone isolation: mutating the returned Fields must not affect the store.
	got.Fields["network"] = json.RawMessage(`"tampered"`)

	again, err := m.GetEndpoint(ctx, "p", "us-central1", "ep")
	if err != nil {
		t.Fatalf("GetEndpoint: %v", err)
	}

	if string(again.Fields["network"]) != `"default"` {
		t.Fatalf("store aliased by returned value: %s", again.Fields["network"])
	}
}

func TestEndpointListScoping(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	for _, id := range []string{"a", "b"} {
		if _, _, err := m.CreateEndpoint(ctx, &idsdriver.Config{
			Project: "p", Location: "us-central1", ID: id,
		}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	// An endpoint in a different region must not leak into the scope.
	if _, _, err := m.CreateEndpoint(ctx, &idsdriver.Config{
		Project: "p", Location: "europe-west1", ID: "c",
	}); err != nil {
		t.Fatalf("create c: %v", err)
	}

	list, err := m.ListEndpoints(ctx, "p", "us-central1")
	if err != nil {
		t.Fatalf("ListEndpoints: %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("list scoped to region should be 2, got %d", len(list))
	}
}

func TestEndpointPatchMasked(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, _, err := m.CreateEndpoint(ctx, &idsdriver.Config{
		Project: "p", Location: "us-central1", ID: "ep",
		Fields: rawFields(map[string]string{"network": "default", "severity": "HIGH"}),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	_, op, err := m.PatchEndpoint(ctx, &idsdriver.Config{
		Project: "p", Location: "us-central1", ID: "ep",
		Fields: map[string]json.RawMessage{"threatExceptions": json.RawMessage(`["12345"]`)},
	}, []string{"threatExceptions"})
	if err != nil {
		t.Fatalf("PatchEndpoint: %v", err)
	}

	if !op.Done || op.Type != "update" {
		t.Fatalf("op = %+v", op)
	}

	got, err := m.GetEndpoint(ctx, "p", "us-central1", "ep")
	if err != nil {
		t.Fatalf("GetEndpoint: %v", err)
	}

	if string(got.Fields["threatExceptions"]) != `["12345"]` {
		t.Fatalf("threatExceptions not patched: %s", got.Fields["threatExceptions"])
	}

	// Unmasked field survives.
	if string(got.Fields["network"]) != `"default"` {
		t.Fatalf("unmasked network mutated: %s", got.Fields["network"])
	}
}

func TestEndpointDeleteAndNotFound(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, err := m.GetEndpoint(ctx, "p", "us-central1", "ghost"); !cerrors.IsNotFound(err) {
		t.Fatalf("GetEndpoint missing err = %v, want NotFound", err)
	}

	if _, _, err := m.CreateEndpoint(ctx, &idsdriver.Config{
		Project: "p", Location: "us-central1", ID: "ep",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	op, err := m.DeleteEndpoint(ctx, "p", "us-central1", "ep")
	if err != nil {
		t.Fatalf("DeleteEndpoint: %v", err)
	}

	if !op.Done || op.Type != "delete" {
		t.Fatalf("op = %+v", op)
	}

	if _, err := m.GetEndpoint(ctx, "p", "us-central1", "ep"); !cerrors.IsNotFound(err) {
		t.Fatalf("expected NotFound after delete, got %v", err)
	}

	if _, err := m.DeleteEndpoint(ctx, "p", "us-central1", "ep"); !cerrors.IsNotFound(err) {
		t.Fatalf("delete missing err = %v, want NotFound", err)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, _, err := m.CreateEndpoint(ctx, &idsdriver.Config{
		Project: "p", Location: "us-central1", ID: "ep",
		Fields: rawFields(map[string]string{"network": "default", "severity": "HIGH"}),
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

	got, err := restored.GetEndpoint(ctx, "p", "us-central1", "ep")
	if err != nil {
		t.Fatalf("GetEndpoint after restore: %v", err)
	}

	if string(got.Fields["severity"]) != `"HIGH"` {
		t.Fatalf("restored endpoint missing severity: %s", got.Fields["severity"])
	}
}
