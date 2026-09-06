package clouddeploy

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cdriver "github.com/stackshy/cloudemu/v2/services/clouddeploy/driver"
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

func TestPipelineCRUD(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	cfg := &cdriver.Config{
		Project: "p", Location: "us-central1", ID: "web",
		Fields: fields(map[string]string{"description": "web"}),
	}

	res, op, err := m.CreatePipeline(ctx, cfg)
	if err != nil {
		t.Fatalf("CreatePipeline: %v", err)
	}

	if !op.Done || op.Type != "create" {
		t.Fatalf("op = %+v", op)
	}

	if res.UID == "" || res.CreateTime.IsZero() || res.Etag == "" {
		t.Fatalf("computed fields missing: %+v", res)
	}

	got, err := m.GetPipeline(ctx, "p", "us-central1", "web")
	if err != nil {
		t.Fatalf("GetPipeline: %v", err)
	}

	if got.UID != res.UID || got.Etag != res.Etag {
		t.Fatalf("computed unstable across reads")
	}

	// Duplicate -> AlreadyExists.
	if _, _, err := m.CreatePipeline(ctx, cfg); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate create err = %v, want AlreadyExists", err)
	}

	// Patch description via mask; etag changes.
	patched, _, err := m.PatchPipeline(ctx, &cdriver.Config{
		Project: "p", Location: "us-central1", ID: "web",
		Fields: fields(map[string]string{"description": "web-v2"}),
	}, []string{"description"})
	if err != nil {
		t.Fatalf("PatchPipeline: %v", err)
	}

	if patched.Etag == res.Etag {
		t.Fatalf("etag should change on mutation")
	}

	if string(patched.Fields["description"]) != `"web-v2"` {
		t.Fatalf("description not patched: %s", patched.Fields["description"])
	}

	if _, err := m.DeletePipeline(ctx, "p", "us-central1", "web"); err != nil {
		t.Fatalf("DeletePipeline: %v", err)
	}

	if _, err := m.GetPipeline(ctx, "p", "us-central1", "web"); !cerrors.IsNotFound(err) {
		t.Fatalf("get after delete err = %v, want NotFound", err)
	}
}

func TestMaskLeavesUnmaskedFieldsUntouched(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	_, _, err := m.CreateTarget(ctx, &cdriver.Config{
		Project: "p", Location: "l", ID: "t",
		Fields: fields(map[string]string{"description": "d", "targetLabel": "keep"}),
	})
	if err != nil {
		t.Fatalf("CreateTarget: %v", err)
	}

	got, _, err := m.PatchTarget(ctx, &cdriver.Config{
		Project: "p", Location: "l", ID: "t",
		Fields: fields(map[string]string{"description": "d2"}),
	}, []string{"description"})
	if err != nil {
		t.Fatalf("PatchTarget: %v", err)
	}

	if string(got.Fields["description"]) != `"d2"` {
		t.Fatalf("masked field not updated")
	}

	if string(got.Fields["targetLabel"]) != `"keep"` {
		t.Fatalf("unmasked field mutated: %s", got.Fields["targetLabel"])
	}
}

func TestListScopedAndSorted(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	for _, id := range []string{"c", "a", "b"} {
		if _, _, err := m.CreateTarget(ctx, &cdriver.Config{Project: "p", Location: "l", ID: id}); err != nil {
			t.Fatalf("CreateTarget %s: %v", id, err)
		}
	}

	// A target in a different location must not leak in.
	if _, _, err := m.CreateTarget(ctx, &cdriver.Config{Project: "p", Location: "other", ID: "z"}); err != nil {
		t.Fatalf("CreateTarget z: %v", err)
	}

	list, err := m.ListTargets(ctx, "p", "l")
	if err != nil {
		t.Fatalf("ListTargets: %v", err)
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

	if _, _, err := m.CreatePipeline(ctx, &cdriver.Config{
		Project: "p", Location: "l", ID: "web",
		Fields: fields(map[string]string{"description": "web"}),
	}); err != nil {
		t.Fatalf("CreatePipeline: %v", err)
	}

	snap, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restored := newMock(t)
	if err := restored.Restore(ctx, snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	got, err := restored.GetPipeline(ctx, "p", "l", "web")
	if err != nil {
		t.Fatalf("GetPipeline after restore: %v", err)
	}

	if string(got.Fields["description"]) != `"web"` {
		t.Fatalf("body not restored: %s", got.Fields["description"])
	}
}
