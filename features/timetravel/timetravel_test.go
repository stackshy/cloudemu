package timetravel_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/features/timetravel"
	"github.com/stackshy/cloudemu/v2/persist"
	"github.com/stackshy/cloudemu/v2/providers/aws"
)

// awsWorld is a minimal live-state harness: it captures the current provider's
// whole-emulator state via persist.ExportAll and restores it into a FRESH,
// empty provider via persist.RestoreAll — exactly the reset-then-restore the
// standalone server does on a rewind. Reassigning p on restore means later
// captures observe the restored state.
type awsWorld struct {
	ctx context.Context
	p   *aws.Provider
}

func (w *awsWorld) services() map[string]persist.Services {
	return map[string]persist.Services{"aws": w.p.SnapshotServices()}
}

func (w *awsWorld) capture() ([]byte, error) {
	snap, err := persist.ExportAll(w.ctx, w.services(), persist.Options{IncludeAssets: true})
	if err != nil {
		return nil, err
	}

	return json.Marshal(snap)
}

func (w *awsWorld) restore(state []byte) error {
	var snap persist.Snapshot
	if err := json.Unmarshal(state, &snap); err != nil {
		return err
	}

	w.p = cloudemu.NewAWS() // wipe to empty before loading, like the server

	return persist.RestoreAll(w.ctx, &snap, w.services())
}

func (w *awsWorld) createBucket(t *testing.T, name string) {
	t.Helper()

	if err := w.p.S3.CreateBucket(w.ctx, name); err != nil {
		t.Fatalf("create bucket %q: %v", name, err)
	}
}

func (w *awsWorld) buckets(t *testing.T) map[string]bool {
	t.Helper()

	infos, err := w.p.S3.ListBuckets(w.ctx)
	if err != nil {
		t.Fatalf("list buckets: %v", err)
	}

	set := map[string]bool{}
	for _, b := range infos {
		set[b.Name] = true
	}

	return set
}

// TestRewindAndForkIsolation is the real-user time-travel flow: save a named
// point, mutate past it, rewind back to it, then fork the point into a branch
// and mutate the branch — asserting the original point is untouched.
func TestRewindAndForkIsolation(t *testing.T) {
	w := &awsWorld{ctx: context.Background(), p: cloudemu.NewAWS()}
	clock := config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	reg := timetravel.New(clock, w.capture, w.restore)

	// v1 point: one bucket.
	w.createBucket(t, "b-v1")
	if err := reg.Save("v1"); err != nil {
		t.Fatalf("save v1: %v", err)
	}

	// Mutate past v1, then rewind — the later bucket must be gone, v1 present.
	w.createBucket(t, "b-later")
	if err := reg.Rewind("v1"); err != nil {
		t.Fatalf("rewind v1: %v", err)
	}

	got := w.buckets(t)
	if !got["b-v1"] || got["b-later"] {
		t.Fatalf("after rewind to v1, buckets = %v; want b-v1 present, b-later gone", got)
	}

	// Fork v1 into an independent branch, rewind onto it, and mutate it.
	if err := reg.Fork("v1", "branch"); err != nil {
		t.Fatalf("fork v1->branch: %v", err)
	}

	if err := reg.Rewind("branch"); err != nil {
		t.Fatalf("rewind branch: %v", err)
	}

	w.createBucket(t, "b-branch")
	if err := reg.Save("branch"); err != nil { // re-save the branch with its new state
		t.Fatalf("save branch: %v", err)
	}

	// Rewinding to v1 must NOT see the branch's mutation: fork isolation.
	if err := reg.Rewind("v1"); err != nil {
		t.Fatalf("rewind v1 after branch mutation: %v", err)
	}

	got = w.buckets(t)
	if !got["b-v1"] || got["b-branch"] {
		t.Fatalf("v1 leaked branch state: buckets = %v; want only b-v1", got)
	}
}

// TestListReportsMetadata checks the registry surfaces names, fork lineage, and
// the deterministic clock's timestamp.
func TestListReportsMetadata(t *testing.T) {
	w := &awsWorld{ctx: context.Background(), p: cloudemu.NewAWS()}
	created := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	reg := timetravel.New(config.NewFakeClock(created), w.capture, w.restore)

	if err := reg.Save("base"); err != nil {
		t.Fatalf("save base: %v", err)
	}

	if err := reg.Fork("base", "feature"); err != nil {
		t.Fatalf("fork: %v", err)
	}

	list := reg.List()
	if len(list) != 2 {
		t.Fatalf("list len = %d, want 2", len(list))
	}

	// List is name-sorted: base, feature.
	if list[0].Name != "base" || list[1].Name != "feature" {
		t.Fatalf("list order = %q,%q; want base,feature", list[0].Name, list[1].Name)
	}

	if list[1].ForkedFrom != "base" {
		t.Fatalf("feature.ForkedFrom = %q, want base", list[1].ForkedFrom)
	}

	if !list[0].CreatedAt.Equal(created) {
		t.Fatalf("base.CreatedAt = %v, want %v", list[0].CreatedAt, created)
	}

	if len(list[0].Providers) == 0 || list[0].Providers[0] != "aws" {
		t.Fatalf("base.Providers = %v, want [aws]", list[0].Providers)
	}
}

// TestErrorsAndDelete checks the not-found, duplicate-fork, invalid-name, and
// delete paths carry the right canonical error codes.
func TestErrorsAndDelete(t *testing.T) {
	w := &awsWorld{ctx: context.Background(), p: cloudemu.NewAWS()}
	reg := timetravel.New(nil, w.capture, w.restore) // nil clock -> real clock default

	if err := reg.Rewind("missing"); !cerrors.IsNotFound(err) {
		t.Fatalf("rewind missing: got %v, want NotFound", err)
	}

	if err := reg.Delete("missing"); !cerrors.IsNotFound(err) {
		t.Fatalf("delete missing: got %v, want NotFound", err)
	}

	if err := reg.Save("bad name"); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("save invalid name: got %v, want InvalidArgument", err)
	}

	if err := reg.Save(".."); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("save '..': got %v, want InvalidArgument", err)
	}

	if err := reg.Save("keep"); err != nil {
		t.Fatalf("save keep: %v", err)
	}

	if err := reg.Fork("keep", "keep"); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("fork onto existing name: got %v, want AlreadyExists", err)
	}

	if err := reg.Fork("missing", "new"); !cerrors.IsNotFound(err) {
		t.Fatalf("fork from missing: got %v, want NotFound", err)
	}

	if err := reg.Delete("keep"); err != nil {
		t.Fatalf("delete keep: %v", err)
	}

	if len(reg.List()) != 0 {
		t.Fatalf("list after delete = %v, want empty", reg.List())
	}
}
