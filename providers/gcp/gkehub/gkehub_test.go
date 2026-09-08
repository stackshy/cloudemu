package gkehub

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	gdriver "github.com/stackshy/cloudemu/v2/services/gkehub/driver"
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

func TestMembershipCRUD(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	cfg := &gdriver.Config{
		Project: "p", Location: "global", ID: "member",
		Fields: fields(map[string]string{"description": "prod cluster", "externalId": "ext-1"}),
	}

	res, op, err := m.CreateMembership(ctx, cfg)
	if err != nil {
		t.Fatalf("CreateMembership: %v", err)
	}

	if !op.Done || op.Type != "create" {
		t.Fatalf("op = %+v", op)
	}

	if res.UID == "" || res.CreateTime.IsZero() || res.UpdateTime.IsZero() {
		t.Fatalf("computed fields missing: %+v", res)
	}

	got, err := m.GetMembership(ctx, "p", "global", "member")
	if err != nil {
		t.Fatalf("GetMembership: %v", err)
	}

	if string(got.Fields["description"]) != `"prod cluster"` {
		t.Fatalf("description not stored: %s", got.Fields["description"])
	}

	// Deep-clone isolation: mutating a returned resource must not touch the store.
	got.Fields["description"] = json.RawMessage(`"tampered"`)

	reGot, _ := m.GetMembership(ctx, "p", "global", "member")
	if string(reGot.Fields["description"]) != `"prod cluster"` {
		t.Fatalf("clone isolation broken: %s", reGot.Fields["description"])
	}

	// Duplicate create -> AlreadyExists.
	if _, _, err := m.CreateMembership(ctx, cfg); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate create err = %v, want AlreadyExists", err)
	}

	// Patch a single masked field.
	_, _, err = m.PatchMembership(ctx, &gdriver.Config{
		Project: "p", Location: "global", ID: "member",
		Fields: fields(map[string]string{"description": "updated"}),
	}, []string{"description"})
	if err != nil {
		t.Fatalf("PatchMembership: %v", err)
	}

	patched, _ := m.GetMembership(ctx, "p", "global", "member")
	if string(patched.Fields["description"]) != `"updated"` {
		t.Fatalf("patch not applied: %s", patched.Fields["description"])
	}

	if string(patched.Fields["externalId"]) != `"ext-1"` {
		t.Fatalf("unmasked field lost: %s", patched.Fields["externalId"])
	}

	if _, err := m.DeleteMembership(ctx, "p", "global", "member"); err != nil {
		t.Fatalf("DeleteMembership: %v", err)
	}

	if _, err := m.GetMembership(ctx, "p", "global", "member"); !cerrors.IsNotFound(err) {
		t.Fatalf("get after delete err = %v, want NotFound", err)
	}
}

func TestFeatureAndFleetCRUD(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, _, err := m.CreateFeature(ctx, &gdriver.Config{
		Project: "p", Location: "global", ID: "multiclusteringress",
		Fields: map[string]json.RawMessage{"spec": json.RawMessage(`{"multiclusteringress":{"configMembership":"m"}}`)},
	}); err != nil {
		t.Fatalf("CreateFeature: %v", err)
	}

	feat, err := m.GetFeature(ctx, "p", "global", "multiclusteringress")
	if err != nil {
		t.Fatalf("GetFeature: %v", err)
	}

	if string(feat.Fields["spec"]) != `{"multiclusteringress":{"configMembership":"m"}}` {
		t.Fatalf("feature spec not round-tripped: %s", feat.Fields["spec"])
	}

	if _, _, err := m.CreateFleet(ctx, &gdriver.Config{
		Project: "p", Location: "global", ID: "default",
		Fields: fields(map[string]string{"displayName": "prod fleet"}),
	}); err != nil {
		t.Fatalf("CreateFleet: %v", err)
	}

	if _, err := m.GetFleet(ctx, "p", "global", "default"); err != nil {
		t.Fatalf("GetFleet: %v", err)
	}

	// Not-found on a missing patch/delete.
	if _, _, err := m.PatchFeature(ctx, &gdriver.Config{Project: "p", Location: "global", ID: "nope"}, nil); !cerrors.IsNotFound(err) {
		t.Fatalf("patch missing feature err = %v, want NotFound", err)
	}
}

func TestListScopedByLocation(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	mk := func(loc, id string) {
		if _, _, err := m.CreateFeature(ctx, &gdriver.Config{Project: "p", Location: loc, ID: id}); err != nil {
			t.Fatalf("CreateFeature(%s,%s): %v", loc, id, err)
		}
	}

	mk("global", "a")
	mk("global", "b")
	mk("us-central1", "c")

	got, err := m.ListFeatures(ctx, "p", "global")
	if err != nil {
		t.Fatalf("ListFeatures: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("global features = %d, want 2", len(got))
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	src := newMock(t)
	ctx := context.Background()

	if _, _, err := src.CreateMembership(ctx, &gdriver.Config{
		Project: "p", Location: "global", ID: "m1",
		Fields: fields(map[string]string{"description": "d"}),
	}); err != nil {
		t.Fatalf("seed membership: %v", err)
	}

	if _, _, err := src.CreateFleet(ctx, &gdriver.Config{Project: "p", Location: "global", ID: "default"}); err != nil {
		t.Fatalf("seed fleet: %v", err)
	}

	data, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := newMock(t)
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	got, err := dst.GetMembership(ctx, "p", "global", "m1")
	if err != nil {
		t.Fatalf("GetMembership after restore: %v", err)
	}

	if string(got.Fields["description"]) != `"d"` {
		t.Fatalf("restored field mismatch: %s", got.Fields["description"])
	}

	if _, err := dst.GetFleet(ctx, "p", "global", "default"); err != nil {
		t.Fatalf("GetFleet after restore: %v", err)
	}
}
