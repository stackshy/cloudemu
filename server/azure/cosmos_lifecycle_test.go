// This file contains  suite tests for the DATABASE cell
// (azure / sdk-compat): real-user journeys driving the official
// azure-sdk-for-go azcosmos client against the CloudEmu Azure server mounted
// in an httptest TLS server.
//
// The Cosmos HTTP surface (server/azure/cosmos/handler.go) covers: account
// probe, virtual databases, container create/list/read/delete, document
// create/list/read/replace/delete, and query (SQL body ignored — full scan).
// TTL and the change feed are driver-level only (no HTTP endpoint), so those
// journeys mix SDK writes with direct driver calls on the same provider, per
// the suite SDK-test-setup notes.
//
// Emulator divergences from real Cosmos that these tests pin down (asserting
// the emulator's actual, documented behavior, marked DIVERGENCE below):
//   - CreateItem with a duplicate id succeeds (blind upsert; real Cosmos: 409).
//   - ReplaceItem ignores IfMatchEtag (real Cosmos: 412 on stale etag).
//   - DeleteItem on a missing document returns 204 (real Cosmos: 404).
//   - Queries ignore the SQL text and the partition key (full scan).
//   - Item identity is the partition-key value only, so two docs with the
//     same pk but different ids collide (real Cosmos keys on (pk, id)).
//   - PATCH (PatchItem) is not routed at all (405 MethodNotAllowed).
package azure_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	azureprovider "github.com/stackshy/cloudemu/v2/providers/azure"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
)

// cosmosSuiteKey is a dummy base64 master key; the handler ignores auth.
const cosmosSuiteKey = "Y2FtcGFpZ24ta2V5" // base64("suite-key")

// cosmosEnv bundles a fresh emulator provider (for driver-level TTL/feed
// calls) with a real azcosmos client pointed at the httptest server.
type cosmosEnv struct {
	provider *azureprovider.Provider
	client   *azcosmos.Client
}

// newCosmosEnv builds a fresh Azure provider (with any config options, e.g. a
// fake clock), mounts only the CosmosDB driver, and returns a real azcosmos
// SDK client. The server MUST be TLS for the Cosmos SDK; MaxRetries: -1
// disables retries so error assertions see the first response.
func newCosmosEnv(t *testing.T, opts ...config.Option) *cosmosEnv {
	t.Helper()

	cloudP := cloudemu.NewAzure(opts...)
	srv := azureserver.New(azureserver.Drivers{CosmosDB: cloudP.CosmosDB})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	cred, err := azcosmos.NewKeyCredential(cosmosSuiteKey)
	if err != nil {
		t.Fatalf("NewKeyCredential: %v", err)
	}

	client, err := azcosmos.NewClientWithKey(ts.URL, cred, &azcosmos.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	})
	if err != nil {
		t.Fatalf("NewClientWithKey: %v", err)
	}

	return &cosmosEnv{provider: cloudP, client: client}
}

// container creates database db (virtual) and container name with partition
// key path "/pk" (required — per-document GETs/DELETEs hardcode the "pk"
// attribute) and returns its client.
func (e *cosmosEnv) container(ctx context.Context, t *testing.T, db, name string) *azcosmos.ContainerClient {
	t.Helper()

	if _, err := e.client.CreateDatabase(ctx, azcosmos.DatabaseProperties{ID: db}, nil); err != nil {
		t.Fatalf("CreateDatabase(%s): %v", db, err)
	}

	dbClient, err := e.client.NewDatabase(db)
	if err != nil {
		t.Fatalf("NewDatabase(%s): %v", db, err)
	}

	props := azcosmos.ContainerProperties{
		ID:                     name,
		PartitionKeyDefinition: azcosmos.PartitionKeyDefinition{Paths: []string{"/pk"}},
	}
	if _, err := dbClient.CreateContainer(ctx, props, nil); err != nil {
		t.Fatalf("CreateContainer(%s): %v", name, err)
	}

	cc, err := dbClient.NewContainer(name)
	if err != nil {
		t.Fatalf("NewContainer(%s): %v", name, err)
	}

	return cc
}

// createDoc marshals doc and creates it under partition key pk.
func createDoc(ctx context.Context, t *testing.T, cc *azcosmos.ContainerClient, pk string, doc map[string]any) {
	t.Helper()

	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal doc: %v", err)
	}

	resp, err := cc.CreateItem(ctx, azcosmos.NewPartitionKeyString(pk), b, nil)
	if err != nil {
		t.Fatalf("CreateItem(%v): %v", doc["id"], err)
	}

	if resp.RawResponse.StatusCode != 201 {
		t.Errorf("CreateItem(%v) status=%d want 201", doc["id"], resp.RawResponse.StatusCode)
	}
}

// readDoc fetches a document and decodes it into a map.
func readDoc(ctx context.Context, t *testing.T, cc *azcosmos.ContainerClient, pk, id string) map[string]any {
	t.Helper()

	resp, err := cc.ReadItem(ctx, azcosmos.NewPartitionKeyString(pk), id, nil)
	if err != nil {
		t.Fatalf("ReadItem(%s): %v", id, err)
	}

	var got map[string]any
	if err := json.Unmarshal(resp.Value, &got); err != nil {
		t.Fatalf("unmarshal ReadItem(%s): %v", id, err)
	}

	return got
}

// wantRespErr asserts err is an azcore.ResponseError with the given HTTP status.
func wantRespErr(t *testing.T, err error, status int, op string) {
	t.Helper()

	if err == nil {
		t.Fatalf("%s: expected error with status %d, got nil", op, status)
	}

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		t.Fatalf("%s: expected *azcore.ResponseError, got %T: %v", op, err, err)
	}

	if respErr.StatusCode != status {
		t.Errorf("%s: status=%d want %d (err=%v)", op, respErr.StatusCode, status, err)
	}
}

// TestCosmosLifecycle walks the full happy path a real user
// follows: database → container → put documents with varied attribute types
// (strings, numbers, bool, null, nested object, array, empty string,
// large-ish value) → point reads → upsert-update → replace → delete document
// → delete container.
func TestCosmosLifecycle(t *testing.T) {
	ctx := context.Background()
	env := newCosmosEnv(t)
	cc := env.container(ctx, t, "shopdb", "orders")

	// Container Read echoes back the partition key definition we created with.
	contResp, err := cc.Read(ctx, nil)
	if err != nil {
		t.Fatalf("Container.Read: %v", err)
	}

	paths := contResp.ContainerProperties.PartitionKeyDefinition.Paths
	if len(paths) != 1 || paths[0] != "/pk" {
		t.Errorf("container partition key paths=%v want [/pk]", paths)
	}

	// Document with varied attribute types.
	large := strings.Repeat("v", 64*1024) // 64 KiB, well under the 5 MB body cap
	order := map[string]any{
		"id":       "ord-1",
		"pk":       "cust-1",
		"sku":      "widget-9",
		"qty":      3,
		"price":    19.99,
		"rush":     true,
		"coupon":   nil,
		"note":     "", // empty string value must round-trip
		"tags":     []any{"a", "b", "c"},
		"shipping": map[string]any{"city": "Berlin", "zip": "10115"},
		"blob":     large,
	}
	createDoc(ctx, t, cc, "cust-1", order)

	got := readDoc(ctx, t, cc, "cust-1", "ord-1")

	if got["sku"] != "widget-9" {
		t.Errorf("sku=%v want widget-9", got["sku"])
	}
	// JSON numbers decode to float64 on the SDK side.
	if got["qty"] != float64(3) {
		t.Errorf("qty=%v want 3", got["qty"])
	}

	if got["price"] != 19.99 {
		t.Errorf("price=%v want 19.99", got["price"])
	}

	if got["rush"] != true {
		t.Errorf("rush=%v want true", got["rush"])
	}

	if v, present := got["coupon"]; !present || v != nil {
		t.Errorf("coupon=%v (present=%v) want explicit null", v, present)
	}

	if got["note"] != "" {
		t.Errorf("note=%q want empty string", got["note"])
	}

	tags, _ := got["tags"].([]any)
	if len(tags) != 3 || tags[0] != "a" {
		t.Errorf("tags=%v want [a b c]", got["tags"])
	}

	ship, _ := got["shipping"].(map[string]any)
	if ship["city"] != "Berlin" {
		t.Errorf("shipping.city=%v want Berlin", got["shipping"])
	}

	if b, _ := got["blob"].(string); len(b) != len(large) {
		t.Errorf("blob length=%d want %d", len(got["blob"].(string)), len(large))
	}

	// Cosmos system properties are synthesized on reads.
	if got["_etag"] == nil || got["_ts"] == nil {
		t.Errorf("expected synthetic _etag/_ts, got etag=%v ts=%v", got["_etag"], got["_ts"])
	}

	// Update via UpsertItem (same identity, changed field).
	order["qty"] = 5
	b, _ := json.Marshal(order)

	if _, err := cc.UpsertItem(ctx, azcosmos.NewPartitionKeyString("cust-1"), b, nil); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	if got := readDoc(ctx, t, cc, "cust-1", "ord-1"); got["qty"] != float64(5) {
		t.Errorf("after upsert qty=%v want 5", got["qty"])
	}

	// Replace via ReplaceItem.
	order["sku"] = "widget-10"
	b, _ = json.Marshal(order)

	if _, err := cc.ReplaceItem(ctx, azcosmos.NewPartitionKeyString("cust-1"), "ord-1", b, nil); err != nil {
		t.Fatalf("ReplaceItem: %v", err)
	}

	if got := readDoc(ctx, t, cc, "cust-1", "ord-1"); got["sku"] != "widget-10" {
		t.Errorf("after replace sku=%v want widget-10", got["sku"])
	}

	// Delete document, then reading it must 404.
	if _, err := cc.DeleteItem(ctx, azcosmos.NewPartitionKeyString("cust-1"), "ord-1", nil); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}

	_, err = cc.ReadItem(ctx, azcosmos.NewPartitionKeyString("cust-1"), "ord-1", nil)
	wantRespErr(t, err, 404, "ReadItem after delete")

	// Delete container, then reading through it must 404.
	if _, err := cc.Delete(ctx, nil); err != nil {
		t.Fatalf("Container.Delete: %v", err)
	}

	_, err = cc.ReadItem(ctx, azcosmos.NewPartitionKeyString("cust-1"), "ord-1", nil)
	wantRespErr(t, err, 404, "ReadItem after container delete")
}

// TestCosmosTypedErrors exercises the SDK-visible typed error
// paths: missing documents, missing containers, duplicate container create,
// and a document without the mandatory id field.
func TestCosmosTypedErrors(t *testing.T) {
	ctx := context.Background()
	env := newCosmosEnv(t)
	cc := env.container(ctx, t, "errdb", "things")

	pk := azcosmos.NewPartitionKeyString("p1")

	// Read a document that never existed → 404.
	_, err := cc.ReadItem(ctx, pk, "ghost", nil)
	wantRespErr(t, err, 404, "ReadItem missing doc")

	// Operations against a container that never existed → 404.
	dbClient, err := env.client.NewDatabase("errdb")
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}

	noSuch, err := dbClient.NewContainer("no-such-container")
	if err != nil {
		t.Fatalf("NewContainer: %v", err)
	}

	_, err = noSuch.ReadItem(ctx, pk, "any", nil)
	wantRespErr(t, err, 404, "ReadItem missing container")

	doc, _ := json.Marshal(map[string]any{"id": "x", "pk": "p1"})
	_, err = noSuch.CreateItem(ctx, pk, doc, nil)
	wantRespErr(t, err, 404, "CreateItem missing container")

	_, err = noSuch.Delete(ctx, nil)
	wantRespErr(t, err, 404, "Delete missing container")

	// Duplicate container create → 409 Conflict.
	dup := azcosmos.ContainerProperties{
		ID:                     "things",
		PartitionKeyDefinition: azcosmos.PartitionKeyDefinition{Paths: []string{"/pk"}},
	}
	_, err = dbClient.CreateContainer(ctx, dup, nil)
	wantRespErr(t, err, 409, "duplicate CreateContainer")

	// Document without an "id" field → 400 BadRequest.
	noID, _ := json.Marshal(map[string]any{"pk": "p1", "name": "anonymous"})
	_, err = cc.CreateItem(ctx, pk, noID, nil)
	wantRespErr(t, err, 400, "CreateItem without id")
}

// TestCosmosWriteDivergences pins down the emulator's write
// semantics where they knowingly diverge from real Cosmos (see file header):
// duplicate create succeeds, etag preconditions are ignored, deleting a
// missing document succeeds, PATCH is not routed, and item identity collides
// on the partition-key value alone.
func TestCosmosWriteDivergences(t *testing.T) {
	ctx := context.Background()
	env := newCosmosEnv(t)
	cc := env.container(ctx, t, "divdb", "users")

	pk := azcosmos.NewPartitionKeyString("team-a")
	createDoc(ctx, t, cc, "team-a", map[string]any{"id": "u1", "pk": "team-a", "name": "Alice", "rev": 1})

	// DIVERGENCE: creating the same id again is a blind upsert and succeeds
	// with 201 (real Cosmos returns 409 Conflict). "Conditional write
	// failure" is therefore unreachable on this emulator.
	dup, _ := json.Marshal(map[string]any{"id": "u1", "pk": "team-a", "name": "Alice-2", "rev": 2})

	resp, err := cc.CreateItem(ctx, pk, dup, nil)
	if err != nil {
		t.Fatalf("duplicate CreateItem (emulator upserts): %v", err)
	}

	if resp.RawResponse.StatusCode != 201 {
		t.Errorf("duplicate CreateItem status=%d want 201", resp.RawResponse.StatusCode)
	}

	if got := readDoc(ctx, t, cc, "team-a", "u1"); got["rev"] != float64(2) {
		t.Errorf("rev=%v want 2 (second create should have overwritten)", got["rev"])
	}

	// DIVERGENCE: ReplaceItem with a deliberately stale IfMatchEtag succeeds
	// (real Cosmos returns 412 PreconditionFailed). ConditionExpression-style
	// guards are not implemented anywhere in the driver.
	stale := azcore.ETag(`"definitely-stale-etag"`)
	repl, _ := json.Marshal(map[string]any{"id": "u1", "pk": "team-a", "name": "Alice-3", "rev": 3})

	if _, err := cc.ReplaceItem(ctx, pk, "u1", repl, &azcosmos.ItemOptions{IfMatchEtag: &stale}); err != nil {
		t.Fatalf("ReplaceItem with stale etag (emulator ignores preconditions): %v", err)
	}

	if got := readDoc(ctx, t, cc, "team-a", "u1"); got["rev"] != float64(3) {
		t.Errorf("rev=%v want 3 (stale-etag replace should have applied)", got["rev"])
	}

	// DIVERGENCE: PATCH is not routed by the handler → 405 (real Cosmos
	// supports partial document updates).
	patch := azcosmos.PatchOperations{}
	patch.AppendSet("/name", "Patched")
	_, err = cc.PatchItem(ctx, pk, "u1", patch, nil)
	wantRespErr(t, err, 405, "PatchItem (unrouted PATCH)")

	// DIVERGENCE: deleting a document that does not exist succeeds with 204
	// (real Cosmos returns 404) — the driver delete is idempotent.
	delResp, err := cc.DeleteItem(ctx, pk, "never-existed", nil)
	if err != nil {
		t.Fatalf("DeleteItem missing doc (emulator is idempotent): %v", err)
	}

	if delResp.RawResponse.StatusCode != 204 {
		t.Errorf("DeleteItem missing doc status=%d want 204", delResp.RawResponse.StatusCode)
	}

	// DIVERGENCE: item identity is the partition-key value only. Two docs
	// with the same pk but different ids share one slot, and a point read for
	// the first id returns the second document (real Cosmos keys on (pk,id)).
	createDoc(ctx, t, cc, "shared", map[string]any{"id": "doc-a", "pk": "shared", "who": "first"})
	createDoc(ctx, t, cc, "shared", map[string]any{"id": "doc-b", "pk": "shared", "who": "second"})

	got := readDoc(ctx, t, cc, "shared", "doc-a")
	if got["id"] != "doc-b" || got["who"] != "second" {
		t.Errorf("pk-collision read got id=%v who=%v; emulator keys items by pk only, expected doc-b/second",
			got["id"], got["who"])
	}
}

// TestCosmosQueryAndPagination drives the SDK query pager: empty
// container first, then 30 documents across two partitions. The emulator
// ignores the SQL text and the partition key (full scan, DIVERGENCE) and
// never emits a continuation token, so everything arrives in one page.
func TestCosmosQueryAndPagination(t *testing.T) {
	ctx := context.Background()
	env := newCosmosEnv(t)
	cc := env.container(ctx, t, "querydb", "events")

	pkA := azcosmos.NewPartitionKeyString("part-a")

	countAll := func(label string) int {
		t.Helper()

		total, pages := 0, 0
		pager := cc.NewQueryItemsPager("SELECT * FROM c WHERE c.kind = 'never-matches'", pkA, nil)

		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				t.Fatalf("%s: NextPage: %v", label, err)
			}

			pages++
			total += len(page.Items)
		}

		if pages > 1 {
			t.Logf("%s: handler emitted continuation across %d pages", label, pages)
		}

		return total
	}

	// Query on an empty container → zero items, no error.
	if n := countAll("empty query"); n != 0 {
		t.Errorf("query on empty container returned %d items, want 0", n)
	}

	// Insert 30 documents: 15 in part-a, 15 in part-b.
	seen := map[string]bool{}

	for i := 0; i < 30; i++ {
		part := "part-a"
		if i%2 == 1 {
			part = "part-b"
		}

		id := "evt-" + string(rune('a'+i/10)) + string(rune('0'+i%10)) // evt-a0..evt-c9, unique ids
		seen[id] = false
		createDoc(ctx, t, cc, part, map[string]any{"id": id, "pk": id, "part": part, "n": i})
	}

	// DIVERGENCE: the WHERE clause and the partition key are both ignored —
	// all 30 documents come back regardless.
	pager := cc.NewQueryItemsPager("SELECT * FROM c WHERE c.part = 'part-a'", pkA, nil)
	total := 0

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		for _, raw := range page.Items {
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("unmarshal query item: %v", err)
			}

			id, _ := doc["id"].(string)

			was, known := seen[id]
			if !known {
				t.Errorf("query returned unknown id %q", id)
				continue
			}

			if was {
				t.Errorf("query returned id %q twice", id)
			}

			seen[id] = true
			total += 1
		}
	}

	if total != 30 {
		t.Errorf("query returned %d items, want all 30 (SQL is ignored → full scan)", total)
	}

	for id, ok := range seen {
		if !ok {
			t.Errorf("query never returned id %q", id)
		}
	}
}

// TestCosmosUnicode round-trips unicode container content: ids,
// partition keys, and values containing CJK, Greek, and emoji.
func TestCosmosUnicode(t *testing.T) {
	ctx := context.Background()
	env := newCosmosEnv(t)
	cc := env.container(ctx, t, "unidb", "translations")

	const (
		docID = "café-π-1"
		part  = "组-α"
	)

	pk := azcosmos.NewPartitionKeyString(part)
	createDoc(ctx, t, cc, part, map[string]any{
		"id":     docID,
		"pk":     part,
		"phrase": "こんにちは世界 🚀 Ünïcode",
		"emoji":  "🎉🎊",
	})

	got := readDoc(ctx, t, cc, part, docID)

	if got["phrase"] != "こんにちは世界 🚀 Ünïcode" {
		t.Errorf("phrase=%q, unicode value did not round-trip", got["phrase"])
	}

	if got["emoji"] != "🎉🎊" {
		t.Errorf("emoji=%q, emoji value did not round-trip", got["emoji"])
	}

	if got["id"] != docID {
		t.Errorf("id=%q want %q", got["id"], docID)
	}

	// Delete by unicode id + pk, then read must 404.
	if _, err := cc.DeleteItem(ctx, pk, docID, nil); err != nil {
		t.Fatalf("DeleteItem unicode id: %v", err)
	}

	_, err := cc.ReadItem(ctx, pk, docID, nil)
	wantRespErr(t, err, 404, "ReadItem deleted unicode doc")
}

// TestCosmosTTL exercises TTL expiry deterministically with the
// injectable fake clock. TTL configuration is driver-only (no Cosmos HTTP
// endpoint), so the config calls go through provider.CosmosDB directly, while
// document writes/reads flow through the real SDK — expiry is SDK-visible as
// a 404. Also pins BatchGetItems skipping the TTL check (documented).
func TestCosmosTTL(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	clk := config.NewFakeClock(start)

	env := newCosmosEnv(t, config.WithClock(clk))
	cc := env.container(ctx, t, "ttldb", "sessions")
	drv := env.provider.CosmosDB

	// Two sessions expiring 5 minutes from the fake "now" (absolute epoch
	// seconds). Distinct pk values because item identity is pk-only.
	expires := start.Add(5 * time.Minute).Unix()
	createDoc(ctx, t, cc, "s1", map[string]any{"id": "s1", "pk": "s1", "user": "alice", "expiresAt": expires})
	createDoc(ctx, t, cc, "s2", map[string]any{"id": "s2", "pk": "s2", "user": "bob", "expiresAt": expires})

	// Enable TTL at the driver level and read the config back.
	if err := drv.UpdateTTL(ctx, "sessions", dbdriver.TTLConfig{Enabled: true, AttributeName: "expiresAt"}); err != nil {
		t.Fatalf("UpdateTTL: %v", err)
	}

	ttlCfg, err := drv.DescribeTTL(ctx, "sessions")
	if err != nil {
		t.Fatalf("DescribeTTL: %v", err)
	}

	if !ttlCfg.Enabled || ttlCfg.AttributeName != "expiresAt" {
		t.Errorf("DescribeTTL=%+v want Enabled+expiresAt", ttlCfg)
	}

	// Before expiry the SDK still sees the document.
	if got := readDoc(ctx, t, cc, "s1", "s1"); got["user"] != "alice" {
		t.Errorf("pre-expiry user=%v want alice", got["user"])
	}

	// Advance past the TTL. Expiry is lazy: the next SDK read observes 404
	// and the item is deleted.
	clk.Advance(10 * time.Minute)

	_, err = cc.ReadItem(ctx, azcosmos.NewPartitionKeyString("s1"), "s1", nil)
	wantRespErr(t, err, 404, "ReadItem expired session")

	// Documented quirk: BatchGetItems does NOT check TTL, so the expired s2
	// (never point-read since expiry) is still returned by a batch get.
	batch, err := drv.BatchGetItems(ctx, "sessions", []map[string]any{{"pk": "s2"}})
	if err != nil {
		t.Fatalf("BatchGetItems: %v", err)
	}

	if len(batch) != 1 || batch[0]["user"] != "bob" {
		t.Errorf("BatchGetItems on expired item=%v want the (unreaped) bob session", batch)
	}

	// A query/scan through the SDK filters the expired document out.
	pager := cc.NewQueryItemsPager("SELECT * FROM c", azcosmos.NewPartitionKeyString("s2"), nil)
	live := 0

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		live += len(page.Items)
	}

	if live != 0 {
		t.Errorf("query after expiry returned %d items, want 0", live)
	}

	// And the point read reaps it.
	_, err = cc.ReadItem(ctx, azcosmos.NewPartitionKeyString("s2"), "s2", nil)
	wantRespErr(t, err, 404, "ReadItem expired s2")
}

// TestCosmosChangeFeed exercises the driver-level change feed
// (there is no Cosmos HTTP change-feed endpoint) fed by REAL SDK writes:
// enable the feed, then create/upsert/delete through azcosmos, and verify
// INSERT/MODIFY/REMOVE records, sequence numbers, the 'lease-000' shard,
// fake-clock timestamps, and token-based resumption.
func TestCosmosChangeFeed(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 7, 2, 8, 30, 0, 0, time.UTC)
	clk := config.NewFakeClock(start)

	env := newCosmosEnv(t, config.WithClock(clk))
	cc := env.container(ctx, t, "feeddb", "chats")
	drv := env.provider.CosmosDB

	// Feed disabled → FailedPrecondition.
	if _, err := drv.GetStreamRecords(ctx, "chats", 0, ""); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("GetStreamRecords before enable: err=%v want FailedPrecondition", err)
	}

	if err := drv.UpdateStreamConfig(ctx, "chats", dbdriver.StreamConfig{
		Enabled:  true,
		ViewType: "NEW_AND_OLD_IMAGES",
	}); err != nil {
		t.Fatalf("UpdateStreamConfig: %v", err)
	}

	// Three SDK writes → INSERT, MODIFY, REMOVE.
	pk := azcosmos.NewPartitionKeyString("room-1")
	createDoc(ctx, t, cc, "room-1", map[string]any{"id": "m1", "pk": "room-1", "text": "hello"})

	upd, _ := json.Marshal(map[string]any{"id": "m1", "pk": "room-1", "text": "hello, edited"})
	if _, err := cc.UpsertItem(ctx, pk, upd, nil); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	if _, err := cc.DeleteItem(ctx, pk, "m1", nil); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}

	it, err := drv.GetStreamRecords(ctx, "chats", 0, "")
	if err != nil {
		t.Fatalf("GetStreamRecords: %v", err)
	}

	if it.ShardID != "lease-000" {
		t.Errorf("ShardID=%q want lease-000", it.ShardID)
	}

	if len(it.Records) != 3 {
		t.Fatalf("got %d feed records, want 3: %+v", len(it.Records), it.Records)
	}

	wantTypes := []string{"INSERT", "MODIFY", "REMOVE"}
	for i, rec := range it.Records {
		if rec.EventType != wantTypes[i] {
			t.Errorf("record %d EventType=%s want %s", i, rec.EventType, wantTypes[i])
		}

		if wantSeq := string(rune('1' + i)); rec.SequenceNumber != wantSeq {
			t.Errorf("record %d SequenceNumber=%q want %q", i, rec.SequenceNumber, wantSeq)
		}

		if !rec.Timestamp.Equal(start) {
			t.Errorf("record %d Timestamp=%v want fake-clock %v", i, rec.Timestamp, start)
		}
	}

	// Image capture per NEW_AND_OLD_IMAGES.
	if v := it.Records[0].NewImage["text"]; v != "hello" {
		t.Errorf("INSERT NewImage text=%v want hello", v)
	}

	if it.Records[0].OldImage != nil {
		t.Errorf("INSERT OldImage=%v want nil", it.Records[0].OldImage)
	}

	if v := it.Records[1].OldImage["text"]; v != "hello" {
		t.Errorf("MODIFY OldImage text=%v want hello", v)
	}

	if v := it.Records[1].NewImage["text"]; v != "hello, edited" {
		t.Errorf("MODIFY NewImage text=%v want 'hello, edited'", v)
	}

	if v := it.Records[2].OldImage["text"]; v != "hello, edited" {
		t.Errorf("REMOVE OldImage text=%v want 'hello, edited'", v)
	}

	if it.Records[2].NewImage != nil {
		t.Errorf("REMOVE NewImage=%v want nil", it.Records[2].NewImage)
	}

	// Token-based resumption: first page of 2, then the remainder.
	page1, err := drv.GetStreamRecords(ctx, "chats", 2, "")
	if err != nil {
		t.Fatalf("GetStreamRecords page1: %v", err)
	}

	if len(page1.Records) != 2 || page1.NextToken != "2" {
		t.Fatalf("page1 records=%d next=%q want 2 records, token \"2\"", len(page1.Records), page1.NextToken)
	}

	page2, err := drv.GetStreamRecords(ctx, "chats", 2, page1.NextToken)
	if err != nil {
		t.Fatalf("GetStreamRecords page2: %v", err)
	}

	if len(page2.Records) != 1 || page2.Records[0].EventType != "REMOVE" || page2.NextToken != "" {
		t.Fatalf("page2 records=%+v next=%q want single REMOVE, empty token", page2.Records, page2.NextToken)
	}
}
