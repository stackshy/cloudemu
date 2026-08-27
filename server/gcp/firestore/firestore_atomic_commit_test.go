package firestore_test

import (
	"testing"

	"google.golang.org/grpc/codes"
)

// TestFirestoreBatchAtomicOnPreconditionFailure proves a batch commit is
// atomic: when a later write fails its precondition, the earlier writes in the
// same batch are NOT persisted. Previously each write applied immediately, so a
// failure left preceding writes committed.
func TestFirestoreBatchAtomicOnPreconditionFailure(t *testing.T) {
	ctx, client, _ := newDBClient(t, "accounts")

	coll := client.Collection("accounts")

	// Seed an existing document so a later Create (exists=false precondition)
	// in the batch is guaranteed to fail.
	if _, err := coll.Doc("existing").Set(ctx, map[string]any{"v": "seed"}); err != nil {
		t.Fatalf("seed existing: %v", err)
	}

	// write1 would create a fresh document; write2 Creates over an existing
	// document and must fail with ALREADY_EXISTS, aborting the whole commit.
	batch := client.Batch()
	batch.Set(coll.Doc("fresh"), map[string]any{"v": "one"})
	batch.Create(coll.Doc("existing"), map[string]any{"v": "two"})

	if _, err := batch.Commit(ctx); dbSDKCode(err) != codes.AlreadyExists {
		t.Fatalf("batch Commit: code=%v err=%v, want AlreadyExists", dbSDKCode(err), err)
	}

	// The first write must NOT have been persisted (atomic rollback).
	if _, err := coll.Doc("fresh").Get(ctx); dbSDKCode(err) != codes.NotFound {
		t.Errorf("doc 'fresh' should not exist after failed batch, got err=%v", err)
	}

	// The pre-existing document must be untouched by the failed Create.
	snap, err := coll.Doc("existing").Get(ctx)
	if err != nil {
		t.Fatalf("Get existing: %v", err)
	}

	if snap.Data()["v"] != "seed" {
		t.Errorf("existing doc mutated by failed batch: v=%v, want seed", snap.Data()["v"])
	}
}
