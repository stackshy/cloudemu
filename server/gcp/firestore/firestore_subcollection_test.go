package firestore_test

import (
	"errors"
	"sort"
	"testing"

	gcpfirestore "cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

// collectDocIDs drains a DocumentIterator into a sorted slice of document ids.
func collectDocIDs(t *testing.T, it *gcpfirestore.DocumentIterator) []string {
	t.Helper()

	var ids []string

	for {
		snap, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}

		if err != nil {
			t.Fatalf("iterator.Next: %v", err)
		}

		ids = append(ids, snap.Ref.ID)
	}

	sort.Strings(ids)

	return ids
}

// TestFirestoreSubcollectionNamespaceIsolation proves a subcollection is modeled
// as its own namespace: writing cities/SF/landmarks/{gg,bb} must NOT appear when
// listing or querying the parent "cities" collection, and the "landmarks"
// subcollection must list/query its own documents. This is the GCP audit
// finding where a subcollection path collapsed into the top-level table,
// contaminating the parent and 404ing the child.
func TestFirestoreSubcollectionNamespaceIsolation(t *testing.T) {
	ctx, client, _ := newDBClient(t, "cities")

	cities := client.Collection("cities")

	// A top-level city document plus two landmarks under it.
	if _, err := cities.Doc("SF").Set(ctx, map[string]any{"name": "San Francisco"}); err != nil {
		t.Fatalf("Set cities/SF: %v", err)
	}

	landmarks := cities.Doc("SF").Collection("landmarks")
	if _, err := landmarks.Doc("gg").Set(ctx, map[string]any{"name": "Golden Gate"}); err != nil {
		t.Fatalf("Set landmarks/gg: %v", err)
	}

	if _, err := landmarks.Doc("bb").Set(ctx, map[string]any{"name": "Baker Beach"}); err != nil {
		t.Fatalf("Set landmarks/bb: %v", err)
	}

	// Parent collection listing must contain ONLY the city document.
	if got := collectDocIDs(t, cities.Documents(ctx)); len(got) != 1 || got[0] != "SF" {
		t.Errorf("cities.Documents = %v, want [SF] (subcollection docs must not leak)", got)
	}

	// The subcollection lists exactly its own documents.
	if got := collectDocIDs(t, landmarks.Documents(ctx)); len(got) != 2 || got[0] != "bb" || got[1] != "gg" {
		t.Errorf("landmarks.Documents = %v, want [bb gg]", got)
	}

	// A runQuery scoped to the parent collection must also exclude subcollection
	// docs (runQuery must scope by the request parent path, not just the
	// trailing collection id).
	parentQ := collectDocIDs(t, cities.Where("name", ">", "").Documents(ctx))
	if len(parentQ) != 1 || parentQ[0] != "SF" {
		t.Errorf("cities query = %v, want [SF]", parentQ)
	}

	// A runQuery scoped to the subcollection returns its documents.
	subQ := collectDocIDs(t, landmarks.Where("name", ">", "").Documents(ctx))
	if len(subQ) != 2 || subQ[0] != "bb" || subQ[1] != "gg" {
		t.Errorf("landmarks query = %v, want [bb gg]", subQ)
	}

	// Direct-ref CRUD on a subcollection document still resolves correctly.
	snap, err := landmarks.Doc("gg").Get(ctx)
	if err != nil {
		t.Fatalf("Get landmarks/gg: %v", err)
	}

	if snap.Data()["name"] != "Golden Gate" {
		t.Errorf("landmarks/gg name = %v, want Golden Gate", snap.Data()["name"])
	}
}
