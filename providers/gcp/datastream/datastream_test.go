package datastream

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	dsdriver "github.com/stackshy/cloudemu/v2/services/datastream/driver"
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

func TestConnectionProfileCRUD(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	cfg := &dsdriver.Config{
		Project: "p", Location: "us-central1", ID: "src",
		Fields: fields(map[string]string{"displayName": "src"}),
	}

	res, op, err := m.CreateConnectionProfile(ctx, cfg)
	if err != nil {
		t.Fatalf("CreateConnectionProfile: %v", err)
	}

	if !op.Done || op.Type != "create" {
		t.Fatalf("op = %+v", op)
	}

	if res.CreateTime.IsZero() || res.UpdateTime.IsZero() {
		t.Fatalf("computed timestamps missing: %+v", res)
	}

	got, err := m.GetConnectionProfile(ctx, "p", "us-central1", "src")
	if err != nil {
		t.Fatalf("GetConnectionProfile: %v", err)
	}

	if !got.CreateTime.Equal(res.CreateTime) {
		t.Fatalf("createTime unstable across reads")
	}

	// Duplicate -> AlreadyExists.
	if _, _, err := m.CreateConnectionProfile(ctx, cfg); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate create err = %v, want AlreadyExists", err)
	}

	patched, _, err := m.PatchConnectionProfile(ctx, &dsdriver.Config{
		Project: "p", Location: "us-central1", ID: "src",
		Fields: fields(map[string]string{"displayName": "src-v2"}),
	}, []string{"displayName"})
	if err != nil {
		t.Fatalf("PatchConnectionProfile: %v", err)
	}

	if string(patched.Fields["displayName"]) != `"src-v2"` {
		t.Fatalf("displayName not patched: %s", patched.Fields["displayName"])
	}

	if _, err := m.DeleteConnectionProfile(ctx, "p", "us-central1", "src"); err != nil {
		t.Fatalf("DeleteConnectionProfile: %v", err)
	}

	if _, err := m.GetConnectionProfile(ctx, "p", "us-central1", "src"); !cerrors.IsNotFound(err) {
		t.Fatalf("get after delete err = %v, want NotFound", err)
	}
}

func TestStreamStatePassthroughAndPatch(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	// A stream body may carry a state (the wire layer seeds NOT_STARTED when it
	// does not); the provider stores it verbatim.
	created, _, err := m.CreateStream(ctx, &dsdriver.Config{
		Project: "p", Location: "l", ID: "s",
		Fields: fields(map[string]string{"displayName": "s", "state": "NOT_STARTED"}),
	})
	if err != nil {
		t.Fatalf("CreateStream: %v", err)
	}

	if string(created.Fields["state"]) != `"NOT_STARTED"` {
		t.Fatalf("state not stored: %s", created.Fields["state"])
	}

	// A state-masked patch transitions the stream; the display name survives.
	patched, _, err := m.PatchStream(ctx, &dsdriver.Config{
		Project: "p", Location: "l", ID: "s",
		Fields: fields(map[string]string{"state": "RUNNING"}),
	}, []string{"state"})
	if err != nil {
		t.Fatalf("PatchStream: %v", err)
	}

	if string(patched.Fields["state"]) != `"RUNNING"` {
		t.Fatalf("state not patched: %s", patched.Fields["state"])
	}

	if string(patched.Fields["displayName"]) != `"s"` {
		t.Fatalf("unmasked displayName mutated: %s", patched.Fields["displayName"])
	}
}

func TestListScopedAndSorted(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	for _, id := range []string{"c", "a", "b"} {
		if _, _, err := m.CreateStream(ctx, &dsdriver.Config{Project: "p", Location: "l", ID: id}); err != nil {
			t.Fatalf("CreateStream %s: %v", id, err)
		}
	}

	// A stream in a different location must not leak in.
	if _, _, err := m.CreateStream(ctx, &dsdriver.Config{Project: "p", Location: "other", ID: "z"}); err != nil {
		t.Fatalf("CreateStream z: %v", err)
	}

	list, err := m.ListStreams(ctx, "p", "l")
	if err != nil {
		t.Fatalf("ListStreams: %v", err)
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

	if _, _, err := m.CreateConnectionProfile(ctx, &dsdriver.Config{
		Project: "p", Location: "l", ID: "src",
		Fields: fields(map[string]string{"displayName": "src"}),
	}); err != nil {
		t.Fatalf("CreateConnectionProfile: %v", err)
	}

	if _, _, err := m.CreateStream(ctx, &dsdriver.Config{
		Project: "p", Location: "l", ID: "s",
		Fields: fields(map[string]string{"state": "RUNNING"}),
	}); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}

	snap, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restored := newMock(t)
	if err := restored.Restore(ctx, snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	gotProfile, err := restored.GetConnectionProfile(ctx, "p", "l", "src")
	if err != nil {
		t.Fatalf("GetConnectionProfile after restore: %v", err)
	}

	if string(gotProfile.Fields["displayName"]) != `"src"` {
		t.Fatalf("profile body not restored: %s", gotProfile.Fields["displayName"])
	}

	gotStream, err := restored.GetStream(ctx, "p", "l", "s")
	if err != nil {
		t.Fatalf("GetStream after restore: %v", err)
	}

	if string(gotStream.Fields["state"]) != `"RUNNING"` {
		t.Fatalf("stream state not restored: %s", gotStream.Fields["state"])
	}
}

func TestMissingIDRejected(t *testing.T) {
	m := newMock(t)

	if _, _, err := m.CreateStream(context.Background(),
		&dsdriver.Config{Project: "p", Location: "l"}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("empty id err = %v, want InvalidArgument", err)
	}
}
