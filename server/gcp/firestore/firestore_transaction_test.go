package firestore_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	gcpfirestore "cloud.google.com/go/firestore"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestFirestoreRunTransaction proves a read-modify-write RunTransaction
// completes end-to-end (GCP audit finding: beginTransaction/rollback returned
// 501, making RunTransaction impossible).
func TestFirestoreRunTransaction(t *testing.T) {
	ctx, client, _ := newDBClient(t, "accounts")

	coll := client.Collection("accounts")

	if _, err := coll.Doc("a").Set(ctx, map[string]any{"balance": 100}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	err := client.RunTransaction(ctx, func(ctx context.Context, tx *gcpfirestore.Transaction) error {
		snap, err := tx.Get(coll.Doc("a"))
		if err != nil {
			return err
		}

		bal, err := snap.DataAt("balance")
		if err != nil {
			return err
		}

		return tx.Set(coll.Doc("a"), map[string]any{"balance": bal.(int64) + 50})
	})
	if err != nil {
		t.Fatalf("RunTransaction: %v", err)
	}

	snap, err := coll.Doc("a").Get(ctx)
	if err != nil {
		t.Fatalf("Get after tx: %v", err)
	}

	if got := snap.Data()["balance"]; got != int64(150) {
		t.Errorf("balance=%v want int64(150)", got)
	}
}

// TestFirestoreConcurrentTransactionsNoLostUpdates proves the emulator
// enforces optimistic concurrency for RunTransaction: N concurrent
// transactions each reading a counter and writing back current+1 must not
// lose updates. Before the transaction-registry fix, :commit applied every
// transaction's writes unconditionally (no conflict detection at all), so
// concurrent read-modify-write transactions silently raced and the final
// count was well under N.
//
// checkTransactionConflict's read-set check and applyStaged's write were also
// found to run as two separate, unsynchronized store operations (no atomic
// section spanning them): two commits could both pass the conflict check
// before either applied, then both overwrite -- a narrower but still real
// lost-update race that this same assertion catches, just less reliably at
// low contention (empirically ~6/10 runs at concurrency=10 -race -count=10).
// concurrency is set high enough, and repeated across enough attempts per
// goroutine, to make that race reproduce on every run should it regress.
func TestFirestoreConcurrentTransactionsNoLostUpdates(t *testing.T) {
	ctx, client, _ := newDBClient(t, "counters")

	doc := client.Collection("counters").Doc("c1")

	if _, err := doc.Set(ctx, map[string]any{"n": int64(0)}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	const concurrency = 40
	// High contention drives many aborts/retries even under a correct
	// implementation; give each transaction generous headroom above the
	// client's default of 5 so a retry-exhaustion error never masquerades as
	// a lost-update failure.
	const maxAttempts = concurrency * 3

	var wg sync.WaitGroup

	errs := make([]error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)

		go func(idx int) {
			defer wg.Done()

			errs[idx] = client.RunTransaction(ctx, func(ctx context.Context, tx *gcpfirestore.Transaction) error {
				snap, gerr := tx.Get(doc)
				if gerr != nil {
					return gerr
				}

				n, _ := snap.DataAt("n")
				cur, _ := n.(int64)

				return tx.Update(doc, []gcpfirestore.Update{{Path: "n", Value: cur + 1}})
			}, gcpfirestore.MaxAttempts(maxAttempts))
		}(i)
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("transaction %d: %v", i, err)
		}
	}

	final, err := doc.Get(ctx)
	if err != nil {
		t.Fatalf("final Get: %v", err)
	}

	n, _ := final.DataAt("n")
	if n != int64(concurrency) {
		t.Errorf("final n=%v want %d (lost updates: transaction conflict-check and apply are not atomic)", n, concurrency)
	}
}

// TestFirestoreTransactionAbortsOnConflictingWrite deterministically exercises
// the conflict-detection path added to :commit: a transaction that reads a
// document, then loses a race to an out-of-band write before it commits, must
// be aborted (HTTP 409, status ABORTED) with its own write never applied —
// rather than blindly overwriting the conflicting write with a value computed
// from the stale read. Drives the wire protocol directly (beginTransaction /
// batchGet / commit) so the conflict is deterministic instead of depending on
// goroutine timing.
func TestFirestoreTransactionAbortsOnConflictingWrite(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Firestore: cloudP.Firestore})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()

	client, err := gcpfirestore.NewRESTClient(ctx, dbProject,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	coll := client.Collection("txnconflict")
	if _, err := coll.Doc("c1").Set(ctx, map[string]any{"n": int64(1)}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	docName := fmt.Sprintf("projects/%s/databases/(default)/documents/txnconflict/c1", dbProject)
	base := ts.URL + "/v1/projects/" + dbProject + "/databases/(default)/documents"

	// beginTransaction.
	var begun struct {
		Transaction string `json:"transaction"`
	}

	postJSON(t, ts, base+":beginTransaction", map[string]any{}, &begun)

	if begun.Transaction == "" {
		t.Fatal("beginTransaction returned no transaction id")
	}

	// Read the document within the transaction (records it in the read-set).
	var batchGot []map[string]any

	postJSON(t, ts, base+":batchGet", map[string]any{
		"documents":   []string{docName},
		"transaction": begun.Transaction,
	}, &batchGot)

	if len(batchGot) != 1 || batchGot[0]["found"] == nil {
		t.Fatalf("batchGet did not find seeded doc: %+v", batchGot)
	}

	// Out-of-band conflicting write, outside the transaction.
	if _, err := coll.Doc("c1").Set(ctx, map[string]any{"n": int64(99)}); err != nil {
		t.Fatalf("conflicting write: %v", err)
	}

	// Commit the transaction's write -- must be rejected as a conflict.
	commitBody := map[string]any{
		"transaction": begun.Transaction,
		"writes": []map[string]any{{
			"update": map[string]any{
				"name":   docName,
				"fields": map[string]any{"n": map[string]any{"integerValue": "2"}},
			},
		}},
	}

	buf, _ := json.Marshal(commitBody)

	resp, err := ts.Client().Post(base+":commit", "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("commit: %v", err)
	}

	respBody, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("conflicting commit: status=%d want 409: %s", resp.StatusCode, respBody)
	}

	var envelope struct {
		Error struct {
			Status string `json:"status"`
		} `json:"error"`
	}

	if err := json.Unmarshal(respBody, &envelope); err != nil {
		t.Fatalf("decode error envelope: %v: %s", err, respBody)
	}

	if envelope.Error.Status != "ABORTED" {
		t.Errorf("error.status=%q want ABORTED: %s", envelope.Error.Status, respBody)
	}

	// The transaction's write must never have been applied -- the document
	// must still hold the out-of-band writer's value, not the transaction's.
	final, err := coll.Doc("c1").Get(ctx)
	if err != nil {
		t.Fatalf("final Get: %v", err)
	}

	if got := final.Data()["n"]; got != int64(99) {
		t.Errorf("final n=%v want 99 (conflicting transaction write must not have applied)", got)
	}
}

// postJSON POSTs a JSON body to url and decodes the JSON response into out,
// failing the test on any transport, status, or decode error.
func postJSON(t *testing.T, ts *httptest.Server, url string, body, out any) {
	t.Helper()

	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request to %s: %v", url, err)
	}

	resp, err := ts.Client().Post(url, "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}

	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s: status=%d: %s", url, resp.StatusCode, respBody)
	}

	if err := json.Unmarshal(respBody, out); err != nil {
		t.Fatalf("decode response from %s: %v: %s", url, err, respBody)
	}
}
