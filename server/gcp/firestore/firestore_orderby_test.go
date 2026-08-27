package firestore_test

import (
	"sort"
	"testing"

	gcpfirestore "cloud.google.com/go/firestore"
)

// TestFirestoreOrderByExcludesMissingField proves an orderBy query excludes
// documents that lack the ordered field, matching Firestore (a missing field
// means the document is not part of the ordered result set) rather than sorting
// the missing values first and returning them.
func TestFirestoreOrderByExcludesMissingField(t *testing.T) {
	ctx, client, _ := newDBClient(t, "players")

	coll := client.Collection("players")

	// Two documents carry "score"; one does not.
	if _, err := coll.Doc("a").Set(ctx, map[string]any{"score": 10}); err != nil {
		t.Fatalf("Set a: %v", err)
	}

	if _, err := coll.Doc("b").Set(ctx, map[string]any{"score": 20}); err != nil {
		t.Fatalf("Set b: %v", err)
	}

	if _, err := coll.Doc("c").Set(ctx, map[string]any{"name": "no-score"}); err != nil {
		t.Fatalf("Set c: %v", err)
	}

	ids := collectDocIDs(t, coll.OrderBy("score", gcpfirestore.Asc).Documents(ctx))
	sort.Strings(ids)

	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Errorf("orderBy(score) = %v, want [a b] (doc without score must be excluded)", ids)
	}
}
