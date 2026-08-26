package firestore_test

import (
	"errors"
	"testing"

	"google.golang.org/api/iterator"
)

// TestFirestoreUserIDFieldRoundTrips proves a user document field literally
// named "id" survives storage instead of being clobbered by the handler's
// internal document-id bookkeeping (a data-loss bug: the reserved key collided
// with the driver partition key). The field must round-trip on read and be
// queryable, while the document's own id stays independent.
func TestFirestoreUserIDFieldRoundTrips(t *testing.T) {
	ctx, client, _ := newDBClient(t, "records")

	coll := client.Collection("records")

	if _, err := coll.Doc("doc1").Set(ctx, map[string]any{
		"id":   "user-supplied-id",
		"name": "Alice",
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	snap, err := coll.Doc("doc1").Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got := snap.Data()["id"]; got != "user-supplied-id" {
		t.Errorf("data[id] = %v, want user-supplied-id (user field was dropped)", got)
	}

	if snap.Ref.ID != "doc1" {
		t.Errorf("Ref.ID = %q, want doc1 (document id must stay independent of the field)", snap.Ref.ID)
	}

	// The user field must be queryable by its own name.
	it := coll.Where("id", "==", "user-supplied-id").Documents(ctx)

	found, err := it.Next()
	if err != nil {
		t.Fatalf("query where id==user-supplied-id: %v", err)
	}

	if found.Ref.ID != "doc1" {
		t.Errorf("query matched %q, want doc1", found.Ref.ID)
	}

	if _, err := it.Next(); !errors.Is(err, iterator.Done) {
		t.Errorf("query returned extra results, want exactly one: err=%v", err)
	}
}
