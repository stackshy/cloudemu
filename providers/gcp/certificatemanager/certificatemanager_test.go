package certificatemanager

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cmdriver "github.com/stackshy/cloudemu/v2/services/certificatemanager/driver"
)

func newMock(t *testing.T) *Mock {
	t.Helper()

	return New(config.NewOptions(config.WithProjectID("p")))
}

func fields(kv map[string]string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range kv {
		out[k] = json.RawMessage(`"` + v + `"`)
	}

	return out
}

func TestCertificateCRUD(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	cfg := &cmdriver.Config{
		Project: "p", Location: "global", ID: "cert",
		Fields: fields(map[string]string{"description": "leaf"}),
	}

	res, op, err := m.CreateCertificate(ctx, cfg)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}

	if !op.Done || op.Type != "create" {
		t.Fatalf("op = %+v", op)
	}

	if res.CreateTime.IsZero() || res.UpdateTime.IsZero() {
		t.Fatalf("computed timestamps missing: %+v", res)
	}

	got, err := m.GetCertificate(ctx, "p", "global", "cert")
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}

	if string(got.Fields["description"]) != `"leaf"` {
		t.Fatalf("description not stored: %s", got.Fields["description"])
	}

	// Duplicate create -> AlreadyExists.
	if _, _, err := m.CreateCertificate(ctx, cfg); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate create err = %v, want AlreadyExists", err)
	}

	// Deep-clone isolation: mutating the returned Fields must not affect the store.
	got.Fields["description"] = json.RawMessage(`"tampered"`)

	again, err := m.GetCertificate(ctx, "p", "global", "cert")
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}

	if string(again.Fields["description"]) != `"leaf"` {
		t.Fatalf("stored resource aliased by returned copy: %s", again.Fields["description"])
	}

	patched, _, err := m.PatchCertificate(ctx, &cmdriver.Config{
		Project: "p", Location: "global", ID: "cert",
		Fields: fields(map[string]string{"description": "leaf-v2"}),
	}, []string{"description"})
	if err != nil {
		t.Fatalf("PatchCertificate: %v", err)
	}

	if string(patched.Fields["description"]) != `"leaf-v2"` {
		t.Fatalf("patch not applied: %s", patched.Fields["description"])
	}

	if _, err := m.DeleteCertificate(ctx, "p", "global", "cert"); err != nil {
		t.Fatalf("DeleteCertificate: %v", err)
	}

	if _, err := m.GetCertificate(ctx, "p", "global", "cert"); !cerrors.IsNotFound(err) {
		t.Fatalf("get after delete err = %v, want NotFound", err)
	}
}

func TestListScopedByLocation(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	mk := func(loc, id string) {
		if _, _, err := m.CreateDNSAuthorization(ctx, &cmdriver.Config{
			Project: "p", Location: loc, ID: id, Fields: fields(map[string]string{"domain": id + ".com"}),
		}); err != nil {
			t.Fatalf("create %s/%s: %v", loc, id, err)
		}
	}

	mk("global", "a")
	mk("global", "b")
	mk("us-central1", "c")

	got, err := m.ListDNSAuthorizations(ctx, "p", "global")
	if err != nil {
		t.Fatalf("ListDNSAuthorizations: %v", err)
	}

	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("list scope/order wrong: %+v", got)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, _, err := m.CreateCertificateMap(ctx, &cmdriver.Config{
		Project: "p", Location: "global", ID: "m", Fields: fields(map[string]string{"description": "map"}),
	}); err != nil {
		t.Fatalf("CreateCertificateMap: %v", err)
	}

	snap, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restored := newMock(t)
	if err := restored.Restore(ctx, snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	got, err := restored.GetCertificateMap(ctx, "p", "global", "m")
	if err != nil {
		t.Fatalf("GetCertificateMap after restore: %v", err)
	}

	if string(got.Fields["description"]) != `"map"` {
		t.Fatalf("restored fields wrong: %s", got.Fields["description"])
	}
}
