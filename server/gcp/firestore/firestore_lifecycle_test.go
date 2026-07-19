//	suite cell DATABASE / gcp / sdk-compat.
//
// These tests drive the REAL cloud.google.com/go/firestore SDK (REST client)
// against the emulator's GCP HTTP server (httptest), asserting SDK-decoded
// responses and SDK-visible typed errors. Journeys covered: collection/table
// lifecycle, document CRUD with varied attribute types (empty strings,
// unicode, nested maps/arrays, large-ish values), queries (runQuery),
// batched writes (:commit) and batched reads (:batchGet via GetAll),
// conditional writes (Create/Update preconditions), pagination through 30
// docs, and typed error paths. TTL and streams are driver-level only (no
// HTTP surface), so those are exercised on provider.Firestore directly with
// a fake clock per the suite survey.
//
// Known divergences from real Firestore (documented in the suite survey)
// are called out inline; tests that assert real-Firestore semantics the
// emulator does not implement are expected to FAIL and are left in place as
// suite findings.
package firestore_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gcpfirestore "cloud.google.com/go/firestore"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
)

const dbProject = "e2e-db-project"

// newDBClient boots a fresh emulator + GCP server, pre-creates the given
// collections as driver tables (required by the Firestore handler), and
// returns a real Firestore REST SDK client pointed at it plus the underlying
// provider for driver-level assertions.
func newDBClient(t *testing.T, colls ...string) (context.Context, *gcpfirestore.Client, *cloudemuGCPHandle) {
	t.Helper()

	ctx := context.Background()
	cloudP := cloudemu.NewGCP()

	for _, c := range colls {
		if err := cloudP.Firestore.CreateTable(ctx, dbdriver.TableConfig{Name: c, PartitionKey: "id"}); err != nil {
			t.Fatalf("CreateTable(%s): %v", c, err)
		}
	}

	srv := gcpserver.New(gcpserver.Drivers{Firestore: cloudP.Firestore})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client, err := gcpfirestore.NewRESTClient(ctx, dbProject,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return ctx, client, &cloudemuGCPHandle{fs: cloudP.Firestore}
}

// cloudemuGCPHandle wraps the driver so tests can reach driver-level ops
// (table delete, TTL, streams) without importing the provider package type.
type cloudemuGCPHandle struct {
	fs dbdriver.Database
}

// dbSDKCode extracts the canonical gRPC code the SDK surfaces for an error.
// The REST transport wraps HTTP errors in apierror (which implements
// GRPCStatus), and client-side misses are genuine status errors; fall back
// to googleapi HTTP codes for safety.
func dbSDKCode(err error) codes.Code {
	if err == nil {
		return codes.OK
	}

	// Prefer the concrete HTTP status over status.FromError: the REST
	// transport's generic mapping folds 409 into Aborted even when the
	// error body carries ALREADY_EXISTS.
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		switch gerr.Code {
		case http.StatusNotFound:
			return codes.NotFound
		case http.StatusConflict:
			return codes.AlreadyExists
		case http.StatusBadRequest:
			return codes.InvalidArgument
		case http.StatusPreconditionFailed:
			return codes.FailedPrecondition
		}
	}

	if s, ok := status.FromError(err); ok {
		return s.Code()
	}

	return codes.Unknown
}

// dbCollectAll drains a document iterator into id -> data.
func dbCollectAll(t *testing.T, it *gcpfirestore.DocumentIterator) map[string]map[string]any {
	t.Helper()

	out := map[string]map[string]any{}

	for {
		snap, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}

		if err != nil {
			t.Fatalf("iterator.Next: %v", err)
		}

		out[snap.Ref.ID] = snap.Data()
	}

	return out
}

// TestDatabaseLifecycle is the core user journey: create the
// collection, put a document with every supported attribute shape, read it
// back through the SDK, list, overwrite (Set = full replace), delete the
// document, then delete the table and observe SDK-visible NotFound.
func TestDatabaseLifecycle(t *testing.T) {
	ctx, client, h := newDBClient(t, "users")

	big := strings.Repeat("x", 64*1024) // 64 KiB value, well under the 5MB body cap

	doc := client.Collection("users").Doc("u1")

	if _, err := doc.Set(ctx, map[string]any{
		"name":    "Alice",
		"note":    "", // empty string must round-trip as "" not nil
		"age":     30,
		"score":   3.14,
		"active":  true,
		"nothing": nil,
		"tags":    []any{"a", "b", 7},
		"meta":    map[string]any{"city": "Pune", "zip": 411001},
		"blob":    big,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	snap, err := doc.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if !snap.Exists() {
		t.Fatal("snapshot should exist")
	}

	got := snap.Data()

	if got["name"] != "Alice" {
		t.Errorf("name=%v want Alice", got["name"])
	}

	if v, ok := got["note"]; !ok || v != "" {
		t.Errorf("note=%v (present=%v) want empty string", v, ok)
	}

	if got["age"] != int64(30) {
		t.Errorf("age=%v (%T) want int64(30)", got["age"], got["age"])
	}

	if got["score"] != 3.14 {
		t.Errorf("score=%v want 3.14", got["score"])
	}

	if got["active"] != true {
		t.Errorf("active=%v want true", got["active"])
	}

	if v, ok := got["nothing"]; !ok || v != nil {
		t.Errorf("nothing=%v (present=%v) want nil", v, ok)
	}

	tags, ok := got["tags"].([]any)
	if !ok || len(tags) != 3 || tags[0] != "a" || tags[1] != "b" || tags[2] != int64(7) {
		t.Errorf("tags=%#v want [a b 7]", got["tags"])
	}

	meta, ok := got["meta"].(map[string]any)
	if !ok || meta["city"] != "Pune" || meta["zip"] != int64(411001) {
		t.Errorf("meta=%#v want city=Pune zip=411001", got["meta"])
	}

	if s, _ := got["blob"].(string); len(s) != len(big) {
		t.Errorf("blob length=%d want %d", len(s), len(big))
	}

	// Second document, then list the collection via runQuery.
	if _, err := client.Collection("users").Doc("u2").Set(ctx, map[string]any{"name": "Bob"}); err != nil {
		t.Fatalf("Set u2: %v", err)
	}

	all := dbCollectAll(t, client.Collection("users").Documents(ctx))
	if len(all) != 2 || all["u1"] == nil || all["u2"] == nil {
		t.Errorf("list got %d docs (%v), want u1+u2", len(all), keysOfDB(all))
	}

	// Set on an existing doc is a full replace (unconditional upsert).
	if _, err := doc.Set(ctx, map[string]any{"name": "Alice v2", "age": 31}); err != nil {
		t.Fatalf("Set (overwrite): %v", err)
	}

	snap2, err := doc.Get(ctx)
	if err != nil {
		t.Fatalf("Get after overwrite: %v", err)
	}

	got2 := snap2.Data()
	if got2["name"] != "Alice v2" || got2["age"] != int64(31) {
		t.Errorf("after overwrite got %v", got2)
	}

	if _, present := got2["score"]; present {
		t.Errorf("score should be gone after full-replace Set, got %v", got2["score"])
	}

	// Delete the doc; a subsequent Get is a typed NotFound with Exists()==false.
	if _, err := doc.Delete(ctx); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	snap3, err := doc.Get(ctx)
	if code := dbSDKCode(err); code != codes.NotFound {
		t.Errorf("Get after delete: code=%v err=%v, want NotFound", code, err)
	}

	if snap3 != nil && snap3.Exists() {
		t.Error("snapshot after delete should not exist")
	}

	// Drop the table underneath the SDK; collection ops now surface NotFound.
	if err := h.fs.DeleteTable(ctx, "users"); err != nil {
		t.Fatalf("DeleteTable: %v", err)
	}

	_, err = client.Collection("users").Doc("u2").Get(ctx)
	if code := dbSDKCode(err); code != codes.NotFound {
		t.Errorf("Get after table delete: code=%v err=%v, want NotFound", code, err)
	}
}

func keysOfDB(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}

// TestDatabaseTypedErrors covers the SDK-visible error surface:
// missing document, missing collection (never pre-created as a table), and
// idempotent delete of a missing document.
func TestDatabaseTypedErrors(t *testing.T) {
	ctx, client, h := newDBClient(t, "orders")

	// Missing document in an existing collection.
	snap, err := client.Collection("orders").Doc("nope").Get(ctx)
	if code := dbSDKCode(err); code != codes.NotFound {
		t.Errorf("Get missing doc: code=%v err=%v, want NotFound", code, err)
	}

	if snap != nil && snap.Exists() {
		t.Error("missing doc snapshot should report Exists()==false")
	}

	// Missing collection: never created as a driver table.
	_, err = client.Collection("ghost").Doc("x").Get(ctx)
	if code := dbSDKCode(err); code != codes.NotFound {
		t.Errorf("Get in missing collection: code=%v err=%v, want NotFound", code, err)
	}

	// Writing into a missing collection is also NotFound (tables must be
	// pre-created — emulator-specific behavior per survey).
	_, err = client.Collection("ghost").Doc("x").Set(ctx, map[string]any{"a": 1})
	if code := dbSDKCode(err); code != codes.NotFound {
		t.Errorf("Set in missing collection: code=%v err=%v, want NotFound", code, err)
	}

	// Listing a missing collection surfaces NotFound through the iterator.
	_, err = client.Collection("ghost").Documents(ctx).Next()
	if code := dbSDKCode(err); code != codes.NotFound {
		t.Errorf("List missing collection: code=%v err=%v, want NotFound", code, err)
	}

	// Deleting a missing document is idempotent — no error (matches real
	// Firestore's unconditional delete).
	if _, err := client.Collection("orders").Doc("never-existed").Delete(ctx); err != nil {
		t.Errorf("Delete missing doc: %v, want nil", err)
	}

	// Driver-level table errors: duplicate create and missing delete.
	if err := h.fs.CreateTable(ctx, dbdriver.TableConfig{Name: "orders", PartitionKey: "id"}); !cerrors.IsAlreadyExists(err) {
		t.Errorf("duplicate CreateTable: %v, want AlreadyExists", err)
	}

	if err := h.fs.DeleteTable(ctx, "no-such-table"); !cerrors.IsNotFound(err) {
		t.Errorf("DeleteTable missing: %v, want NotFound", err)
	}
}

// TestDatabaseEmptyCollectionQuery: querying a pre-created but
// empty collection yields an immediately-done iterator, no error.
func TestDatabaseEmptyCollectionQuery(t *testing.T) {
	ctx, client, _ := newDBClient(t, "empty")

	all := dbCollectAll(t, client.Collection("empty").Documents(ctx))
	if len(all) != 0 {
		t.Errorf("empty collection returned %d docs: %v", len(all), keysOfDB(all))
	}
}

// TestDatabasePagination30Docs writes 30 documents and iterates
// the whole collection through the SDK (runQuery streams all items).
func TestDatabasePagination30Docs(t *testing.T) {
	ctx, client, _ := newDBClient(t, "bulk")

	const n = 30

	for i := 0; i < n; i++ {
		id := fmt.Sprintf("item-%02d", i)
		if _, err := client.Collection("bulk").Doc(id).Set(ctx, map[string]any{"n": i}); err != nil {
			t.Fatalf("Set %s: %v", id, err)
		}
	}

	all := dbCollectAll(t, client.Collection("bulk").Documents(ctx))
	if len(all) != n {
		t.Fatalf("iterated %d docs, want %d", len(all), n)
	}

	for i := 0; i < n; i++ {
		id := fmt.Sprintf("item-%02d", i)

		d, ok := all[id]
		if !ok {
			t.Errorf("missing %s", id)
			continue
		}

		if d["n"] != int64(i) {
			t.Errorf("%s: n=%v want %d", id, d["n"], i)
		}
	}
}

// TestDatabaseUnicode: unicode document IDs, field names, and
// values must round-trip through the REST wire format.
func TestDatabaseUnicode(t *testing.T) {
	ctx, client, _ := newDBClient(t, "i18n")

	const docID = "café-日本語-🚀"

	doc := client.Collection("i18n").Doc(docID)

	if _, err := doc.Set(ctx, map[string]any{
		"名前":    "アリス",
		"emoji": "✨🎉",
		"mixed": "ASCII + ümlaut + 中文",
	}); err != nil {
		t.Fatalf("Set unicode: %v", err)
	}

	snap, err := doc.Get(ctx)
	if err != nil {
		t.Fatalf("Get unicode: %v", err)
	}

	got := snap.Data()
	if got["名前"] != "アリス" || got["emoji"] != "✨🎉" || got["mixed"] != "ASCII + ümlaut + 中文" {
		t.Errorf("unicode round-trip mismatch: %v", got)
	}

	all := dbCollectAll(t, client.Collection("i18n").Documents(ctx))
	if _, ok := all[docID]; !ok {
		t.Errorf("unicode doc id not in listing: %v", keysOfDB(all))
	}

	if _, err := doc.Delete(ctx); err != nil {
		t.Errorf("Delete unicode doc: %v", err)
	}
}

// TestDatabaseBatchWriteAndGetAll exercises the batched paths the
// SDK actually uses: WriteBatch → documents:commit with multiple writes, and
// GetAll → documents:batchGet with found + missing entries.
func TestDatabaseBatchWriteAndGetAll(t *testing.T) {
	ctx, client, _ := newDBClient(t, "batch")

	coll := client.Collection("batch")

	// Seed a doc that the batch will delete.
	if _, err := coll.Doc("d3").Set(ctx, map[string]any{"v": "delete-me"}); err != nil {
		t.Fatalf("seed d3: %v", err)
	}

	// One commit with two sets and one delete.
	batch := client.Batch()
	batch.Set(coll.Doc("d1"), map[string]any{"v": "one"})
	batch.Set(coll.Doc("d2"), map[string]any{"v": "two"})
	batch.Delete(coll.Doc("d3"))

	if _, err := batch.Commit(ctx); err != nil {
		t.Fatalf("batch Commit: %v", err)
	}

	s1, err := coll.Doc("d1").Get(ctx)
	if err != nil || s1.Data()["v"] != "one" {
		t.Errorf("d1 after batch: err=%v data=%v", err, s1.Data())
	}

	s2, err := coll.Doc("d2").Get(ctx)
	if err != nil || s2.Data()["v"] != "two" {
		t.Errorf("d2 after batch: err=%v data=%v", err, s2.Data())
	}

	if _, err := coll.Doc("d3").Get(ctx); dbSDKCode(err) != codes.NotFound {
		t.Errorf("d3 should be deleted by batch, got err=%v", err)
	}

	// Batched read: two hits + one miss. Real Firestore returns snapshots in
	// request order with Exists()==false for the miss.
	snaps, err := client.GetAll(ctx, []*gcpfirestore.DocumentRef{
		coll.Doc("d1"), coll.Doc("d2"), coll.Doc("missing"),
	})
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}

	if len(snaps) != 3 {
		t.Fatalf("GetAll returned %d snaps, want 3", len(snaps))
	}

	// Nil-guard: the emulator's :batchGet handler frames each entry as its
	// own JSON array, so the SDK's REST stream decoder stops after the first
	// entry and later snapshots come back nil.
	for i, s := range snaps {
		if s == nil {
			t.Errorf("GetAll[%d] is nil (batchGet response framing drops entries after the first)", i)
		}
	}

	if snaps[0] != nil && (!snaps[0].Exists() || snaps[0].Data()["v"] != "one") {
		t.Errorf("GetAll[0]: exists=%v data=%v", snaps[0].Exists(), snaps[0].Data())
	}

	if snaps[1] != nil && (!snaps[1].Exists() || snaps[1].Data()["v"] != "two") {
		t.Errorf("GetAll[1]: exists=%v data=%v", snaps[1].Exists(), snaps[1].Data())
	}

	if snaps[2] != nil && snaps[2].Exists() {
		t.Errorf("GetAll[2] should be missing, got %v", snaps[2].Data())
	}
}

// TestDatabaseConditionalWrites asserts real-Firestore
// precondition semantics through the SDK. KNOWN DIVERGENCE (suite
// survey: "No conditional writes anywhere"): the emulator's :commit handler
// drops currentDocument preconditions and PutItem is a blind upsert, so the
// failure-path assertions below are EXPECTED TO FAIL against the emulator.
// Documents current emulator behavior.
func TestDatabaseConditionalWrites(t *testing.T) {
	ctx, client, _ := newDBClient(t, "cond")

	doc := client.Collection("cond").Doc("c1")

	// Conditional-create success path: Create on a fresh doc.
	if _, err := doc.Create(ctx, map[string]any{"v": int64(1)}); err != nil {
		t.Fatalf("Create (new doc): %v", err)
	}

	// Conditional-create failure path: real Firestore returns AlreadyExists.
	_, err := doc.Create(ctx, map[string]any{"v": int64(2)})
	if code := dbSDKCode(err); code != codes.AlreadyExists {
		t.Errorf("Create on existing doc: code=%v err=%v, want AlreadyExists (emulator ignores exists=false precondition)", code, err)
	}

	// Conditional-update failure path: Update on a missing doc must be
	// NotFound in real Firestore (exists=true precondition).
	_, err = client.Collection("cond").Doc("never").Update(ctx, []gcpfirestore.Update{{Path: "v", Value: 9}})
	if code := dbSDKCode(err); code != codes.NotFound {
		t.Errorf("Update on missing doc: code=%v err=%v, want NotFound (emulator upserts instead)", code, err)
	}
}

// TestDatabaseUpdateReplaceSemantics documents the emulator's
// survey-listed divergence: field-masked Update (and PATCH) fully REPLACE
// the stored document instead of merging, so unmentioned fields are lost.
func TestDatabaseUpdateReplaceSemantics(t *testing.T) {
	ctx, client, _ := newDBClient(t, "merge")

	doc := client.Collection("merge").Doc("m1")

	if _, err := doc.Set(ctx, map[string]any{"keep": "original", "change": "old"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if _, err := doc.Update(ctx, []gcpfirestore.Update{{Path: "change", Value: "new"}}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	snap, err := doc.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	got := snap.Data()

	if got["change"] != "new" {
		t.Errorf("change=%v want new", got["change"])
	}

	// DIVERGENCE (survey: "PATCH and :commit Set fully REPLACE the document,
	// no field-mask merge semantics"): real Firestore would preserve "keep";
	// the emulator drops it. Assert the documented emulator behavior.
	if v, present := got["keep"]; present {
		t.Logf("NOTE: 'keep'=%v survived Update — emulator gained merge semantics? Survey says full replace.", v)
		t.Errorf("expected survey-documented full-replace behavior (keep dropped), but keep=%v is present", v)
	}
}

// TestDatabaseQueryFiltersIgnored documents the survey-listed
// behavior that documents:runQuery only honors from.collectionId — where
// clauses are ignored and every document comes back (full scan).
func TestDatabaseQueryFiltersIgnored(t *testing.T) {
	ctx, client, _ := newDBClient(t, "accts")

	coll := client.Collection("accts")
	for i, age := range []int{10, 20, 30} {
		id := fmt.Sprintf("a%d", i)
		if _, err := coll.Doc(id).Set(ctx, map[string]any{"age": age}); err != nil {
			t.Fatalf("Set %s: %v", id, err)
		}
	}

	// Real Firestore would return 1 doc (age > 25); the emulator's runQuery
	// ignores structuredQuery.where entirely → full scan of 3 docs.
	it := coll.Where("age", ">", 25).Documents(ctx)

	got := dbCollectAll(t, it)
	if len(got) != 3 {
		t.Errorf("filtered query returned %d docs (%v); survey documents filters-ignored full scan of 3", len(got), keysOfDB(got))
	}
}

// TestDatabaseNumericRoundTrip pins the wire-format numeric
// behaviors: ints stay int64, non-integer floats stay float64, and — per the
// survey — integer-valued float64s are re-encoded as integerValue, so a Go
// float64(2) comes back as int64(2) through the SDK.
func TestDatabaseNumericRoundTrip(t *testing.T) {
	ctx, client, _ := newDBClient(t, "nums")

	doc := client.Collection("nums").Doc("n1")

	if _, err := doc.Set(ctx, map[string]any{
		"i":    42,
		"neg":  -7,
		"f":    3.5,
		"fint": float64(2), // sent as doubleValue 2
		"zero": 0,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	snap, err := doc.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	got := snap.Data()

	if got["i"] != int64(42) {
		t.Errorf("i=%v (%T) want int64(42)", got["i"], got["i"])
	}

	if got["neg"] != int64(-7) {
		t.Errorf("neg=%v want int64(-7)", got["neg"])
	}

	if got["f"] != 3.5 {
		t.Errorf("f=%v (%T) want float64(3.5)", got["f"], got["f"])
	}

	// DIVERGENCE: real Firestore preserves doubleValue; the emulator
	// re-encodes integer-valued floats as integerValue (survey-documented).
	if got["fint"] != int64(2) {
		t.Errorf("fint=%v (%T) want int64(2) per survey-documented integer re-encoding", got["fint"], got["fint"])
	}

	if got["zero"] != int64(0) {
		t.Errorf("zero=%v want int64(0)", got["zero"])
	}
}

// TestDatabaseTTLFakeClock exercises TTL deterministically at the
// driver level (TTL has no Firestore HTTP surface) using the injectable
// clock: items past their absolute unix-seconds TTL are invisible to
// GetItem/Scan (lazy delete), while BatchGetItems skips the TTL check.
func TestDatabaseTTLFakeClock(t *testing.T) {
	ctx := context.Background()
	base := time.Unix(1_700_000_000, 0).UTC()
	fc := config.NewFakeClock(base)

	cloudP := cloudemu.NewGCP(config.WithClock(fc))
	fs := cloudP.Firestore

	if err := fs.CreateTable(ctx, dbdriver.TableConfig{Name: "sessions", PartitionKey: "id"}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	if err := fs.UpdateTTL(ctx, "sessions", dbdriver.TTLConfig{Enabled: true, AttributeName: "expires"}); err != nil {
		t.Fatalf("UpdateTTL: %v", err)
	}

	ttlCfg, err := fs.DescribeTTL(ctx, "sessions")
	if err != nil || !ttlCfg.Enabled || ttlCfg.AttributeName != "expires" {
		t.Fatalf("DescribeTTL: cfg=%+v err=%v", ttlCfg, err)
	}

	// s1 expires in 60s; s2 expires in 1h.
	if err := fs.PutItem(ctx, "sessions", map[string]any{"id": "s1", "expires": base.Unix() + 60, "who": "short"}); err != nil {
		t.Fatalf("PutItem s1: %v", err)
	}

	if err := fs.PutItem(ctx, "sessions", map[string]any{"id": "s2", "expires": base.Unix() + 3600, "who": "long"}); err != nil {
		t.Fatalf("PutItem s2: %v", err)
	}

	// Before expiry both are visible.
	if _, err := fs.GetItem(ctx, "sessions", map[string]any{"id": "s1"}); err != nil {
		t.Fatalf("GetItem s1 before expiry: %v", err)
	}

	// Advance past s1's TTL but not s2's.
	fc.Advance(2 * time.Minute)

	// BatchGetItems does NOT check TTL (survey) — expired s1 still returned.
	batch, err := fs.BatchGetItems(ctx, "sessions", []map[string]any{{"id": "s1"}, {"id": "s2"}})
	if err != nil {
		t.Fatalf("BatchGetItems: %v", err)
	}

	if len(batch) != 2 {
		t.Errorf("BatchGetItems returned %d items, want 2 (TTL not checked on batch reads)", len(batch))
	}

	// Scan filters the expired item.
	scan, err := fs.Scan(ctx, dbdriver.ScanInput{Table: "sessions"})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if scan.Count != 1 || len(scan.Items) != 1 || fmt.Sprintf("%v", scan.Items[0]["id"]) != "s2" {
		t.Errorf("Scan after expiry: count=%d items=%v, want only s2", scan.Count, scan.Items)
	}

	// GetItem observes expiry, returns NotFound, and lazily deletes s1.
	if _, err := fs.GetItem(ctx, "sessions", map[string]any{"id": "s1"}); !cerrors.IsNotFound(err) {
		t.Errorf("GetItem expired s1: %v, want NotFound", err)
	}

	// After the lazy delete even BatchGetItems no longer sees s1.
	batch2, err := fs.BatchGetItems(ctx, "sessions", []map[string]any{{"id": "s1"}, {"id": "s2"}})
	if err != nil {
		t.Fatalf("BatchGetItems after lazy delete: %v", err)
	}

	if len(batch2) != 1 || fmt.Sprintf("%v", batch2[0]["id"]) != "s2" {
		t.Errorf("BatchGetItems after lazy delete: %v, want only s2", batch2)
	}

	// s2 remains readable.
	if _, err := fs.GetItem(ctx, "sessions", map[string]any{"id": "s2"}); err != nil {
		t.Errorf("GetItem s2: %v", err)
	}
}

// TestDatabaseStreams exercises the driver-level change feed
// (no HTTP surface): FailedPrecondition until enabled, INSERT/MODIFY/REMOVE
// events with monotonic sequence numbers, view-type image capture,
// fake-clock timestamps, and sequence-number token pagination.
func TestDatabaseStreams(t *testing.T) {
	ctx := context.Background()
	base := time.Unix(1_700_000_000, 0).UTC()
	fc := config.NewFakeClock(base)

	cloudP := cloudemu.NewGCP(config.WithClock(fc))
	fs := cloudP.Firestore

	if err := fs.CreateTable(ctx, dbdriver.TableConfig{Name: "feed", PartitionKey: "id"}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	// Streams disabled → FailedPrecondition.
	if _, err := fs.GetStreamRecords(ctx, "feed", 0, ""); !cerrors.IsFailedPrecondition(err) {
		t.Errorf("GetStreamRecords before enable: %v, want FailedPrecondition", err)
	}

	if err := fs.UpdateStreamConfig(ctx, "feed", dbdriver.StreamConfig{Enabled: true, ViewType: "NEW_AND_OLD_IMAGES"}); err != nil {
		t.Fatalf("UpdateStreamConfig: %v", err)
	}

	if err := fs.PutItem(ctx, "feed", map[string]any{"id": "d1", "v": "one"}); err != nil {
		t.Fatalf("PutItem insert: %v", err)
	}

	if err := fs.PutItem(ctx, "feed", map[string]any{"id": "d1", "v": "two"}); err != nil {
		t.Fatalf("PutItem modify: %v", err)
	}

	if err := fs.DeleteItem(ctx, "feed", map[string]any{"id": "d1"}); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}

	it, err := fs.GetStreamRecords(ctx, "feed", 0, "")
	if err != nil {
		t.Fatalf("GetStreamRecords: %v", err)
	}

	if len(it.Records) != 3 {
		t.Fatalf("got %d stream records, want 3", len(it.Records))
	}

	wantTypes := []string{"INSERT", "MODIFY", "REMOVE"}
	for i, rec := range it.Records {
		if rec.EventType != wantTypes[i] {
			t.Errorf("record %d type=%s want %s", i, rec.EventType, wantTypes[i])
		}

		wantSeq := fmt.Sprintf("%d", i+1)
		if rec.SequenceNumber != wantSeq {
			t.Errorf("record %d seq=%s want %s", i, rec.SequenceNumber, wantSeq)
		}

		wantID := fmt.Sprintf("event-%d", i+1)
		if rec.EventID != wantID {
			t.Errorf("record %d eventID=%s want %s", i, rec.EventID, wantID)
		}

		if !rec.Timestamp.Equal(base) {
			t.Errorf("record %d timestamp=%v want fake-clock %v", i, rec.Timestamp, base)
		}
	}

	// NEW_AND_OLD_IMAGES capture.
	ins, mod, rem := it.Records[0], it.Records[1], it.Records[2]

	if ins.NewImage == nil || ins.NewImage["v"] != "one" || ins.OldImage != nil {
		t.Errorf("INSERT images: new=%v old=%v", ins.NewImage, ins.OldImage)
	}

	if mod.NewImage == nil || mod.NewImage["v"] != "two" || mod.OldImage == nil || mod.OldImage["v"] != "one" {
		t.Errorf("MODIFY images: new=%v old=%v", mod.NewImage, mod.OldImage)
	}

	if rem.OldImage == nil || rem.OldImage["v"] != "two" || rem.NewImage != nil {
		t.Errorf("REMOVE images: new=%v old=%v", rem.NewImage, rem.OldImage)
	}

	// Token pagination: limit 2 → records 1-2 plus a resume token; resuming
	// yields the final record and an empty token.
	page1, err := fs.GetStreamRecords(ctx, "feed", 2, "")
	if err != nil {
		t.Fatalf("GetStreamRecords page1: %v", err)
	}

	if len(page1.Records) != 2 || page1.NextToken != "2" {
		t.Errorf("page1: %d records token=%q, want 2 records token=2", len(page1.Records), page1.NextToken)
	}

	page2, err := fs.GetStreamRecords(ctx, "feed", 2, page1.NextToken)
	if err != nil {
		t.Fatalf("GetStreamRecords page2: %v", err)
	}

	if len(page2.Records) != 1 || page2.Records[0].SequenceNumber != "3" || page2.NextToken != "" {
		t.Errorf("page2: %d records seq=%v token=%q, want 1 record seq=3 empty token",
			len(page2.Records), page2.Records, page2.NextToken)
	}

	if it.ShardID == "" {
		t.Error("stream iterator should carry a shard id")
	}
}
