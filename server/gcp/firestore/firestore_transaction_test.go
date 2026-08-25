package firestore_test

import (
	"context"
	"testing"

	gcpfirestore "cloud.google.com/go/firestore"
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
