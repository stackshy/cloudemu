// These tests drive the REAL cloud.google.com/go/firestore SDK against the
// emulator's GCP HTTP server, asserting field-transform write semantics
// (serverTimestamp / increment / arrayUnion / arrayRemove), the "!=" absent-
// field exclusion, and currentDocument.updateTime / delete preconditions.
//
// The central regression they guard: a transform-only write (e.g. Update with
// only firestore.Increment) must MERGE onto the stored document, never replace
// it with an empty doc.
package firestore_test

import (
	"context"
	"testing"
	"time"

	gcpfirestore "cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
)

// TestDatabaseIncrementNoDataLoss is the CRITICAL data-loss regression: a
// transform-only Update must increment the target and preserve every other
// field, not wipe the document.
func TestDatabaseIncrementNoDataLoss(t *testing.T) {
	ctx, client, _ := newDBClient(t, "counters")

	doc := client.Collection("counters").Doc("c1")

	if _, err := doc.Set(ctx, map[string]any{"count": 10, "keep": "me"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if _, err := doc.Update(ctx, []gcpfirestore.Update{
		{Path: "count", Value: gcpfirestore.Increment(5)},
	}); err != nil {
		t.Fatalf("Update Increment: %v", err)
	}

	snap, err := doc.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	got := snap.Data()
	if got["count"] != int64(15) {
		t.Errorf("count=%v (%T), want int64(15)", got["count"], got["count"])
	}

	if got["keep"] != "me" {
		t.Errorf("keep=%v, want %q (transform-only write must not wipe other fields)", got["keep"], "me")
	}
}

// TestDatabaseIncrementTyping pins numeric typing: int increments stay int64,
// double increments stay float64, and an increment on an absent field starts
// from zero.
func TestDatabaseIncrementTyping(t *testing.T) {
	ctx, client, _ := newDBClient(t, "typed")

	doc := client.Collection("typed").Doc("t1")

	if _, err := doc.Set(ctx, map[string]any{"i": 10, "f": 1.5}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if _, err := doc.Update(ctx, []gcpfirestore.Update{
		{Path: "i", Value: gcpfirestore.Increment(5)},
		{Path: "f", Value: gcpfirestore.Increment(2.0)},
		{Path: "fresh", Value: gcpfirestore.Increment(7)},
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got := mustGet(ctx, t, doc)

	if got["i"] != int64(15) {
		t.Errorf("i=%v (%T), want int64(15)", got["i"], got["i"])
	}

	if got["f"] != 3.5 {
		t.Errorf("f=%v (%T), want float64(3.5)", got["f"], got["f"])
	}

	// Increment on an absent field starts from 0.
	if got["fresh"] != int64(7) {
		t.Errorf("fresh=%v (%T), want int64(7)", got["fresh"], got["fresh"])
	}
}

// TestDatabaseServerTimestamp asserts a ServerTimestamp sentinel resolves to a
// timestamp near now while the plain field is preserved.
func TestDatabaseServerTimestamp(t *testing.T) {
	ctx, client, _ := newDBClient(t, "stamps")

	doc := client.Collection("stamps").Doc("s1")

	before := time.Now().Add(-time.Minute)

	if _, err := doc.Set(ctx, map[string]any{"n": 1, "at": gcpfirestore.ServerTimestamp}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got := mustGet(ctx, t, doc)

	if got["n"] != int64(1) {
		t.Errorf("n=%v, want int64(1)", got["n"])
	}

	at, ok := got["at"].(time.Time)
	if !ok {
		t.Fatalf("at=%v (%T), want time.Time", got["at"], got["at"])
	}

	if at.Before(before) || at.After(time.Now().Add(time.Minute)) {
		t.Errorf("at=%v not within a sane window of now", at)
	}
}

// TestDatabaseArrayTransforms asserts ArrayUnion appends only missing elements
// and ArrayRemove strips all matches, both merging onto the stored array.
func TestDatabaseArrayTransforms(t *testing.T) {
	ctx, client, _ := newDBClient(t, "arrays")

	doc := client.Collection("arrays").Doc("a1")

	if _, err := doc.Set(ctx, map[string]any{"tags": []string{"a", "b"}, "keep": "x"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if _, err := doc.Update(ctx, []gcpfirestore.Update{
		{Path: "tags", Value: gcpfirestore.ArrayUnion("b", "c")},
	}); err != nil {
		t.Fatalf("ArrayUnion: %v", err)
	}

	got := mustGet(ctx, t, doc)
	if !equalStrs(toStrs(got["tags"]), []string{"a", "b", "c"}) {
		t.Errorf("after ArrayUnion tags=%v, want [a b c]", got["tags"])
	}

	if got["keep"] != "x" {
		t.Errorf("keep=%v, want x (array transform must not wipe siblings)", got["keep"])
	}

	if _, err := doc.Update(ctx, []gcpfirestore.Update{
		{Path: "tags", Value: gcpfirestore.ArrayRemove("a")},
	}); err != nil {
		t.Fatalf("ArrayRemove: %v", err)
	}

	got = mustGet(ctx, t, doc)
	if !equalStrs(toStrs(got["tags"]), []string{"b", "c"}) {
		t.Errorf("after ArrayRemove tags=%v, want [b c]", got["tags"])
	}
}

// TestDatabaseTransformInBatch asserts a transform inside a WriteBatch commit
// applies and merges.
func TestDatabaseTransformInBatch(t *testing.T) {
	ctx, client, _ := newDBClient(t, "bcount")

	doc := client.Collection("bcount").Doc("b1")

	if _, err := doc.Set(ctx, map[string]any{"count": 100, "keep": "y"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	batch := client.Batch()
	batch.Update(doc, []gcpfirestore.Update{{Path: "count", Value: gcpfirestore.Increment(1)}})

	if _, err := batch.Commit(ctx); err != nil {
		t.Fatalf("batch Commit: %v", err)
	}

	got := mustGet(ctx, t, doc)
	if got["count"] != int64(101) {
		t.Errorf("count=%v, want int64(101)", got["count"])
	}

	if got["keep"] != "y" {
		t.Errorf("keep=%v, want y", got["keep"])
	}
}

// TestDatabaseTransformInTransaction asserts a transform inside RunTransaction
// applies and merges.
func TestDatabaseTransformInTransaction(t *testing.T) {
	ctx, client, _ := newDBClient(t, "tcount")

	doc := client.Collection("tcount").Doc("t1")

	if _, err := doc.Set(ctx, map[string]any{"count": 5, "keep": "z"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	err := client.RunTransaction(ctx, func(_ context.Context, tx *gcpfirestore.Transaction) error {
		return tx.Update(doc, []gcpfirestore.Update{{Path: "count", Value: gcpfirestore.Increment(2)}})
	})
	if err != nil {
		t.Fatalf("RunTransaction: %v", err)
	}

	got := mustGet(ctx, t, doc)
	if got["count"] != int64(7) {
		t.Errorf("count=%v, want int64(7)", got["count"])
	}

	if got["keep"] != "z" {
		t.Errorf("keep=%v, want z", got["keep"])
	}
}

// TestDatabaseNotEqualExcludesAbsent asserts a "!=" filter excludes documents
// whose field is absent, matching real Firestore.
func TestDatabaseNotEqualExcludesAbsent(t *testing.T) {
	ctx, client, _ := newDBClient(t, "ne")

	coll := client.Collection("ne")

	if _, err := coll.Doc("has1").Set(ctx, map[string]any{"status": "active"}); err != nil {
		t.Fatalf("Set has1: %v", err)
	}

	if _, err := coll.Doc("has2").Set(ctx, map[string]any{"status": "inactive"}); err != nil {
		t.Fatalf("Set has2: %v", err)
	}

	// Document with NO status field must be excluded from status != "active".
	if _, err := coll.Doc("absent").Set(ctx, map[string]any{"other": "1"}); err != nil {
		t.Fatalf("Set absent: %v", err)
	}

	got := dbCollectAll(t, coll.Where("status", "!=", "active").Documents(ctx))
	if len(got) != 1 {
		t.Errorf("status != active returned %d docs (%v), want 1 (absent-field doc excluded)", len(got), keysOfDB(got))
	}

	if _, ok := got["has2"]; !ok {
		t.Errorf("expected only has2 to match, got %v", keysOfDB(got))
	}
}

// TestDatabaseUpdateTimePrecondition asserts a stale currentDocument.updateTime
// guard fails with FAILED_PRECONDITION while the exact stored time succeeds.
func TestDatabaseUpdateTimePrecondition(t *testing.T) {
	ctx, client, _ := newDBClient(t, "occ")

	doc := client.Collection("occ").Doc("o1")

	if _, err := doc.Set(ctx, map[string]any{"v": 1}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	snap, err := doc.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Stale guard: an update time an hour in the past must be rejected.
	stale := snap.UpdateTime.Add(-time.Hour)

	_, err = doc.Update(ctx,
		[]gcpfirestore.Update{{Path: "v", Value: 2}},
		gcpfirestore.LastUpdateTime(stale))
	if code := dbSDKCode(err); code != codes.FailedPrecondition {
		t.Errorf("stale updateTime guard: code=%v err=%v, want FailedPrecondition", code, err)
	}

	// Matching guard: the exact stored update time must succeed.
	if _, err := doc.Update(ctx,
		[]gcpfirestore.Update{{Path: "v", Value: 3}},
		gcpfirestore.LastUpdateTime(snap.UpdateTime)); err != nil {
		t.Errorf("matching updateTime guard: unexpected err=%v", err)
	}
}

// TestDatabaseDeleteExistsPrecondition asserts a Delete guarded by exists=true
// on a missing document is rejected.
func TestDatabaseDeleteExistsPrecondition(t *testing.T) {
	ctx, client, _ := newDBClient(t, "del")

	missing := client.Collection("del").Doc("ghost")

	_, err := missing.Delete(ctx, gcpfirestore.Exists)
	if code := dbSDKCode(err); code != codes.NotFound {
		t.Errorf("Delete(Exists) on missing doc: code=%v err=%v, want NotFound", code, err)
	}
}

// mustGet fetches a document's data, failing the test on error.
func mustGet(ctx context.Context, t *testing.T, doc *gcpfirestore.DocumentRef) map[string]any {
	t.Helper()

	snap, err := doc.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	return snap.Data()
}

// toStrs coerces a decoded []interface{} of strings to []string.
func toStrs(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}

	out := make([]string, 0, len(arr))
	for _, e := range arr {
		s, _ := e.(string)
		out = append(out, s)
	}

	return out
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}
