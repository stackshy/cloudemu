package tablestorage_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/data/aztables"
)

func marshalEntity(t *testing.T, pk, rk string, extra map[string]any) []byte {
	t.Helper()

	e := map[string]any{"PartitionKey": pk, "RowKey": rk}
	for k, v := range extra {
		e[k] = v
	}

	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal entity: %v", err)
	}

	return b
}

// TestSubmitTransactionApplies covers finding #1: an entity group transaction
// ($batch) inserts and merges entities atomically and the changes are visible.
func TestSubmitTransactionApplies(t *testing.T) {
	ctx := context.Background()
	client, _ := newTableClient(t, "txn")

	// Seed an entity we will merge in the transaction.
	if _, err := client.AddEntity(ctx, marshalEntity(t, "p", "existing", map[string]any{"V": 1}), nil); err != nil {
		t.Fatalf("seed AddEntity: %v", err)
	}

	actions := []aztables.TransactionAction{
		{ActionType: aztables.TransactionTypeAdd, Entity: marshalEntity(t, "p", "a", map[string]any{"V": 10})},
		{ActionType: aztables.TransactionTypeAdd, Entity: marshalEntity(t, "p", "b", map[string]any{"V": 20})},
		{
			ActionType: aztables.TransactionTypeUpdateMerge,
			Entity:     marshalEntity(t, "p", "existing", map[string]any{"W": 2}),
		},
	}

	if _, err := client.SubmitTransaction(ctx, actions, nil); err != nil {
		t.Fatalf("SubmitTransaction: %v", err)
	}

	// The two inserts landed.
	for _, rk := range []string{"a", "b"} {
		if _, err := client.GetEntity(ctx, "p", rk, nil); err != nil {
			t.Errorf("GetEntity %q after batch: %v", rk, err)
		}
	}

	// The merge added W while keeping V.
	got, err := client.GetEntity(ctx, "p", "existing", nil)
	if err != nil {
		t.Fatalf("GetEntity existing: %v", err)
	}

	var props map[string]any
	if err := json.Unmarshal(got.Value, &props); err != nil {
		t.Fatalf("unmarshal existing: %v", err)
	}

	if props["V"] == nil || props["W"] == nil {
		t.Errorf("merged entity = %v, want both V and W present", props)
	}
}

// TestSubmitTransactionRollsBack covers finding #1's atomicity: a failing op
// (insert of an already-existing row) rolls the whole change set back.
func TestSubmitTransactionRollsBack(t *testing.T) {
	ctx := context.Background()
	client, _ := newTableClient(t, "txnrb")

	if _, err := client.AddEntity(ctx, marshalEntity(t, "p", "dup", nil), nil); err != nil {
		t.Fatalf("seed AddEntity: %v", err)
	}

	actions := []aztables.TransactionAction{
		{ActionType: aztables.TransactionTypeAdd, Entity: marshalEntity(t, "p", "fresh", nil)},
		// This insert conflicts with the seeded row and must fail the batch.
		{ActionType: aztables.TransactionTypeAdd, Entity: marshalEntity(t, "p", "dup", nil)},
	}

	if _, err := client.SubmitTransaction(ctx, actions, nil); err == nil {
		t.Fatal("SubmitTransaction with a conflicting insert succeeded, want an error")
	}

	// Because the batch failed atomically, the first insert must NOT have landed.
	if _, err := client.GetEntity(ctx, "p", "fresh", nil); err == nil {
		t.Error("entity 'fresh' exists after a rolled-back batch, want it absent")
	}
}

// buildRawBatch renders a raw multipart/mixed $batch body with one change set of
// insert operations, bypassing the aztables client-side partition-key check so
// the server's own validation can be exercised.
func buildRawBatch(t *testing.T, table string, entities [][]byte) (contentType string, body []byte) {
	t.Helper()

	var changeset bytes.Buffer

	cw := multipart.NewWriter(&changeset)

	for _, ent := range entities {
		part, err := cw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {"application/http"},
			"Content-Transfer-Encoding": {"binary"},
		})
		if err != nil {
			t.Fatalf("create change-set part: %v", err)
		}

		var inner bytes.Buffer

		fmt.Fprintf(&inner, "POST /%s HTTP/1.1\r\n", table)
		inner.WriteString("Content-Type: application/json\r\n")
		fmt.Fprintf(&inner, "Content-Length: %d\r\n\r\n", len(ent))
		inner.Write(ent)

		if _, err := part.Write(inner.Bytes()); err != nil {
			t.Fatalf("write inner request: %v", err)
		}
	}

	_ = cw.Close()

	var batch bytes.Buffer

	bw := multipart.NewWriter(&batch)

	part, err := bw.CreatePart(textproto.MIMEHeader{
		"Content-Type": {"multipart/mixed; boundary=" + cw.Boundary()},
	})
	if err != nil {
		t.Fatalf("create batch part: %v", err)
	}

	if _, err := part.Write(changeset.Bytes()); err != nil {
		t.Fatalf("write change set: %v", err)
	}

	_ = bw.Close()

	return "multipart/mixed; boundary=" + bw.Boundary(), batch.Bytes()
}

// postRawBatch submits a raw $batch body and returns the response body text.
func postRawBatch(t *testing.T, ts, contentType string, body []byte) string {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, ts+"/$batch", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new batch request: %v", err)
	}

	req.Header.Set("Content-Type", contentType)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do batch request: %v", err)
	}
	defer resp.Body.Close()

	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read batch response: %v", err)
	}

	return string(out)
}

// TestBatchMixedPartitionKeysRejected covers the entity-group-transaction rule
// that all operations must share one PartitionKey; a mixed batch is rejected.
func TestBatchMixedPartitionKeysRejected(t *testing.T) {
	_, ts := newTableClient(t, "mixpk")

	contentType, body := buildRawBatch(t, "mixpk", [][]byte{
		marshalEntity(t, "p", "a", nil),
		marshalEntity(t, "q", "b", nil),
	})

	got := postRawBatch(t, ts.URL, contentType, body)
	if !strings.Contains(got, "CommandsInBatchActOnDifferentPartitions") {
		t.Fatalf("mixed-partition batch response = %q, want CommandsInBatchActOnDifferentPartitions", got)
	}
}

// TestBatchDuplicateEntityRejected covers the rule that an entity may appear only
// once in a transaction; a duplicate (PartitionKey, RowKey) is rejected.
func TestBatchDuplicateEntityRejected(t *testing.T) {
	_, ts := newTableClient(t, "dupent")

	contentType, body := buildRawBatch(t, "dupent", [][]byte{
		marshalEntity(t, "p", "a", nil),
		marshalEntity(t, "p", "a", nil),
	})

	got := postRawBatch(t, ts.URL, contentType, body)
	if !strings.Contains(got, "InvalidDuplicateRow") {
		t.Fatalf("duplicate-entity batch response = %q, want InvalidDuplicateRow", got)
	}
}
