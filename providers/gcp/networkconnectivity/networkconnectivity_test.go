package networkconnectivity

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	nccdriver "github.com/stackshy/cloudemu/v2/services/networkconnectivity/driver"
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

func TestHubCRUD(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	cfg := &nccdriver.Config{
		Project: "p", Location: "global", ID: "h1",
		Fields: fields(map[string]string{"description": "hub"}),
	}

	res, op, err := m.CreateHub(ctx, cfg)
	if err != nil {
		t.Fatalf("CreateHub: %v", err)
	}

	if !op.Done || op.Type != "create" {
		t.Fatalf("op = %+v", op)
	}

	if res.UniqueID == "" || res.State != activeState || res.CreateTime.IsZero() || res.UpdateTime.IsZero() {
		t.Fatalf("computed fields missing: %+v", res)
	}

	got, err := m.GetHub(ctx, "p", "global", "h1")
	if err != nil {
		t.Fatalf("GetHub: %v", err)
	}

	if got.UniqueID != res.UniqueID || !got.CreateTime.Equal(res.CreateTime) || got.State != activeState {
		t.Fatalf("computed fields unstable across reads: %+v vs %+v", res, got)
	}

	// Duplicate -> AlreadyExists.
	if _, _, err := m.CreateHub(ctx, cfg); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate create err = %v, want AlreadyExists", err)
	}

	patched, _, err := m.PatchHub(ctx, &nccdriver.Config{
		Project: "p", Location: "global", ID: "h1",
		Fields: fields(map[string]string{"description": "hub-v2"}),
	}, []string{"description"})
	if err != nil {
		t.Fatalf("PatchHub: %v", err)
	}

	if string(patched.Fields["description"]) != `"hub-v2"` {
		t.Fatalf("description not patched: %s", patched.Fields["description"])
	}

	// Immutable computed fields survive a patch.
	if patched.UniqueID != res.UniqueID || !patched.CreateTime.Equal(res.CreateTime) {
		t.Fatalf("patch mutated immutable computed fields")
	}

	if _, err := m.DeleteHub(ctx, "p", "global", "h1"); err != nil {
		t.Fatalf("DeleteHub: %v", err)
	}

	if _, err := m.GetHub(ctx, "p", "global", "h1"); !cerrors.IsNotFound(err) {
		t.Fatalf("get after delete err = %v, want NotFound", err)
	}
}

func TestSpokePatchMasked(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	created, _, err := m.CreateSpoke(ctx, &nccdriver.Config{
		Project: "p", Location: "us-central1", ID: "s",
		Fields: fields(map[string]string{"description": "s", "hub": "h1"}),
	})
	if err != nil {
		t.Fatalf("CreateSpoke: %v", err)
	}

	if created.State != activeState {
		t.Fatalf("spoke minted state = %q, want ACTIVE", created.State)
	}

	patched, _, err := m.PatchSpoke(ctx, &nccdriver.Config{
		Project: "p", Location: "us-central1", ID: "s",
		Fields: fields(map[string]string{"description": "s-v2"}),
	}, []string{"description"})
	if err != nil {
		t.Fatalf("PatchSpoke: %v", err)
	}

	if string(patched.Fields["description"]) != `"s-v2"` {
		t.Fatalf("description not patched: %s", patched.Fields["description"])
	}

	if string(patched.Fields["hub"]) != `"h1"` {
		t.Fatalf("unmasked hub mutated: %s", patched.Fields["hub"])
	}
}

func TestListScopedAndSorted(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	for _, id := range []string{"c", "a", "b"} {
		if _, _, err := m.CreateSpoke(ctx, &nccdriver.Config{Project: "p", Location: "us-central1", ID: id}); err != nil {
			t.Fatalf("CreateSpoke %s: %v", id, err)
		}
	}

	// A spoke in a different location must not leak in.
	if _, _, err := m.CreateSpoke(ctx, &nccdriver.Config{Project: "p", Location: "us-east1", ID: "z"}); err != nil {
		t.Fatalf("CreateSpoke z: %v", err)
	}

	list, err := m.ListSpokes(ctx, "p", "us-central1")
	if err != nil {
		t.Fatalf("ListSpokes: %v", err)
	}

	if len(list) != 3 {
		t.Fatalf("list len = %d, want 3", len(list))
	}

	if list[0].ID != "a" || list[1].ID != "b" || list[2].ID != "c" {
		t.Fatalf("list not sorted by name: %v", []string{list[0].ID, list[1].ID, list[2].ID})
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, _, err := m.CreateHub(ctx, &nccdriver.Config{
		Project: "p", Location: "global", ID: "h1",
		Fields: fields(map[string]string{"description": "hub"}),
	}); err != nil {
		t.Fatalf("CreateHub: %v", err)
	}

	if _, _, err := m.CreateSpoke(ctx, &nccdriver.Config{
		Project: "p", Location: "us-central1", ID: "s",
		Fields: fields(map[string]string{"hub": "h1"}),
	}); err != nil {
		t.Fatalf("CreateSpoke: %v", err)
	}

	snap, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restored := newMock(t)
	if err := restored.Restore(ctx, snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	gotHub, err := restored.GetHub(ctx, "p", "global", "h1")
	if err != nil {
		t.Fatalf("GetHub after restore: %v", err)
	}

	if string(gotHub.Fields["description"]) != `"hub"` || gotHub.UniqueID == "" || gotHub.State != activeState {
		t.Fatalf("hub not restored with computed fields: %+v", gotHub)
	}

	gotSpoke, err := restored.GetSpoke(ctx, "p", "us-central1", "s")
	if err != nil {
		t.Fatalf("GetSpoke after restore: %v", err)
	}

	if string(gotSpoke.Fields["hub"]) != `"h1"` {
		t.Fatalf("spoke body not restored: %s", gotSpoke.Fields["hub"])
	}
}

func TestMissingIDRejected(t *testing.T) {
	m := newMock(t)

	if _, _, err := m.CreateHub(context.Background(),
		&nccdriver.Config{Project: "p", Location: "global"}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("empty id err = %v, want InvalidArgument", err)
	}
}
