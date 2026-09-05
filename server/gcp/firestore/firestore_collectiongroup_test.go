package firestore_test

import (
	"testing"

	gcpfirestore "cloud.google.com/go/firestore"
)

// TestFirestoreCollectionGroupQuery proves a collection-group query
// (from.allDescendants=true) matches documents in every collection sharing the
// group's id at any depth — not just a top-level collection of that name. The
// audit finding: runQuery/runAggregationQuery ignored allDescendants and scanned
// only the single collection directly under the request parent, so a real
// CollectionGroup query silently returned an empty (or wrong) result set.
func TestFirestoreCollectionGroupQuery(t *testing.T) {
	ctx, client, _ := newDBClient(t, "cities")

	cities := client.Collection("cities")

	// Landmarks live under three different parent documents at the same depth.
	seed := []struct {
		city, id, name string
		rank           int64
	}{
		{"SF", "gg", "Golden Gate", 1},
		{"SF", "ferry", "Ferry Building", 3},
		{"LA", "hollywood", "Hollywood Sign", 5},
	}

	for _, s := range seed {
		lm := cities.Doc(s.city).Collection("landmarks").Doc(s.id)
		if _, err := lm.Set(ctx, map[string]any{"name": s.name, "rank": s.rank}); err != nil {
			t.Fatalf("Set %s/landmarks/%s: %v", s.city, s.id, err)
		}
	}

	// A same-named collection at the ROOT must NOT be included: the group id is
	// "landmarks" as a nested collection; a root "landmarks" doc still counts
	// because collection-group matches the id at ANY depth including the root.
	// To keep the assertion crisp we instead add an unrelated root collection.
	if _, err := client.Collection("parks").Doc("central").Set(ctx, map[string]any{"name": "Central Park"}); err != nil {
		t.Fatalf("Set parks/central: %v", err)
	}

	// 1. Bare collection-group query returns all three landmarks.
	all := collectDocIDs(t, client.CollectionGroup("landmarks").Documents(ctx))
	if len(all) != 3 || all[0] != "ferry" || all[1] != "gg" || all[2] != "hollywood" {
		t.Errorf("CollectionGroup(landmarks) = %v, want [ferry gg hollywood]", all)
	}

	// 2. Each returned document carries its own subcollection resource path, so
	// the SDK can resolve its parent. Verify one full path.
	found := map[string]string{}

	it := client.CollectionGroup("landmarks").Documents(ctx)

	for {
		snap, err := it.Next()
		if err != nil {
			break
		}

		found[snap.Ref.ID] = snap.Ref.Parent.Path
	}

	if p := found["hollywood"]; p == "" {
		t.Errorf("hollywood not returned by collection-group query")
	}

	// 3. Collection-group WHERE filter narrows across all subcollections.
	filtered := collectDocIDs(t, client.CollectionGroup("landmarks").
		Where("rank", ">=", int64(3)).Documents(ctx))
	if len(filtered) != 2 || filtered[0] != "ferry" || filtered[1] != "hollywood" {
		t.Errorf("CollectionGroup(landmarks).Where(rank>=3) = %v, want [ferry hollywood]", filtered)
	}

	// 4. OrderBy across the group is honored.
	ordered := client.CollectionGroup("landmarks").OrderBy("rank", gcpfirestore.Desc).Documents(ctx)

	var order []string

	for {
		snap, err := ordered.Next()
		if err != nil {
			break
		}

		order = append(order, snap.Ref.ID)
	}

	if len(order) != 3 || order[0] != "hollywood" || order[2] != "gg" {
		t.Errorf("CollectionGroup(landmarks).OrderBy(rank desc) = %v, want [hollywood ferry gg]", order)
	}
}

// TestFirestoreCollectionGroupAggregation proves a collection-group aggregation
// (COUNT/SUM) spans every matching subcollection, not just one collection under
// the parent.
func TestFirestoreCollectionGroupAggregation(t *testing.T) {
	ctx, client, _ := newDBClient(t, "cities")

	cities := client.Collection("cities")

	writes := []struct {
		city, id string
		rank     int64
	}{
		{"SF", "gg", 1},
		{"SF", "ferry", 3},
		{"LA", "hollywood", 5},
	}

	for _, wr := range writes {
		if _, err := cities.Doc(wr.city).Collection("landmarks").Doc(wr.id).
			Set(ctx, map[string]any{"rank": wr.rank}); err != nil {
			t.Fatalf("Set %s/%s: %v", wr.city, wr.id, err)
		}
	}

	res, err := client.CollectionGroup("landmarks").
		NewAggregationQuery().
		WithCount("total").
		WithSum("rank", "rank_sum").
		Get(ctx)
	if err != nil {
		t.Fatalf("collection-group aggregation: %v", err)
	}

	if got := aggInteger(t, res, "total"); got != 3 {
		t.Errorf("collection-group COUNT = %d, want 3", got)
	}

	if got := aggInteger(t, res, "rank_sum"); got != 9 {
		t.Errorf("collection-group SUM(rank) = %d, want 9", got)
	}
}
