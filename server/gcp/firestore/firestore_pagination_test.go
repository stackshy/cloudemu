package firestore_test

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/api/iterator"
)

// TestFirestoreListDocumentsPagesFully proves a large collection lists every
// document rather than silently truncating at the driver's default limit (GCP
// audit finding: 150-doc collection stops at 100). Both the ListDocuments path
// (DocumentRefs) and the RunQuery path (Documents) must return all 150.
func TestFirestoreListDocumentsPagesFully(t *testing.T) {
	ctx, client, _ := newDBClient(t, "big")

	coll := client.Collection("big")

	const n = 150

	for i := 0; i < n; i++ {
		if _, err := coll.Doc(fmt.Sprintf("d-%03d", i)).Set(ctx, map[string]any{"i": i}); err != nil {
			t.Fatalf("Set %d: %v", i, err)
		}
	}

	// DocumentRefs uses the ListDocuments RPC (GET .../{collection}).
	refs := 0
	it := coll.DocumentRefs(ctx)

	for {
		_, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}

		if err != nil {
			t.Fatalf("DocumentRefs.Next: %v", err)
		}

		refs++
	}

	if refs != n {
		t.Errorf("DocumentRefs listed %d, want %d", refs, n)
	}

	// Documents uses RunQuery; it must also return the full set.
	docs := dbCollectAll(t, coll.Documents(ctx))
	if len(docs) != n {
		t.Errorf("Documents listed %d, want %d", len(docs), n)
	}
}

// TestFirestoreListCollectionIds proves client.Collections enumerates the
// lazily-created collection ids (GCP audit finding: listCollectionIds returned
// 501).
func TestFirestoreListCollectionIds(t *testing.T) {
	ctx, client, _ := newDBClient(t)

	if _, err := client.Collection("users").Doc("u1").Set(ctx, map[string]any{"n": 1}); err != nil {
		t.Fatalf("Set users: %v", err)
	}

	if _, err := client.Collection("orders").Doc("o1").Set(ctx, map[string]any{"n": 1}); err != nil {
		t.Fatalf("Set orders: %v", err)
	}

	seen := map[string]bool{}
	it := client.Collections(ctx)

	for {
		cref, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}

		if err != nil {
			t.Fatalf("Collections.Next: %v", err)
		}

		seen[cref.ID] = true
	}

	if !seen["users"] || !seen["orders"] {
		t.Errorf("collection ids = %v, want users and orders", seen)
	}
}
