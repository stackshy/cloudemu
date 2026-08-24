package tablestorage_test

import (
	"context"
	"encoding/json"
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
