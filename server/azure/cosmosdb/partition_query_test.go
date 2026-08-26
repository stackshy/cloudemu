// This file exercises partition-scoped query isolation: a query pinned to a
// single partition key must see only that partition's documents, while a query
// with no partition key fans out across the whole container. Driven through the
// real azcosmos SDK against the CloudEmu Azure server.

package cosmosdb_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

// queryIDs runs a query and collects the "id" of every returned document across
// all continuation pages.
func queryIDs(ctx context.Context, t *testing.T, cc *azcosmos.ContainerClient, pk azcosmos.PartitionKey, opts *azcosmos.QueryOptions) []string {
	t.Helper()

	var ids []string

	pager := cc.NewQueryItemsPager("SELECT * FROM c", pk, opts)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		for _, raw := range page.Items {
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("unmarshal item: %v", err)
			}

			ids = append(ids, doc["id"].(string))
		}
	}

	return ids
}

// TestSDKCosmosPartitionScopedQuery asserts a partition-scoped SELECT * FROM c
// over a multi-partition container returns ONLY the scoped partition's
// documents, while an unscoped (cross-partition) query returns them all.
func TestSDKCosmosPartitionScopedQuery(t *testing.T) {
	ctx := context.Background()
	env := newCosmosEnv(t)
	cc := env.container(ctx, t, "pqdb", "events")

	// Three documents in team-a, two in team-b.
	createDoc(ctx, t, cc, "team-a", map[string]any{"id": "a1", "pk": "team-a"})
	createDoc(ctx, t, cc, "team-a", map[string]any{"id": "a2", "pk": "team-a"})
	createDoc(ctx, t, cc, "team-a", map[string]any{"id": "a3", "pk": "team-a"})
	createDoc(ctx, t, cc, "team-b", map[string]any{"id": "b1", "pk": "team-b"})
	createDoc(ctx, t, cc, "team-b", map[string]any{"id": "b2", "pk": "team-b"})

	// Scoped to team-a: only its three documents, none from team-b.
	scoped := queryIDs(ctx, t, cc, azcosmos.NewPartitionKeyString("team-a"), nil)
	if len(scoped) != 3 {
		t.Fatalf("scoped query returned %d docs (%v), want 3 (team-a only)", len(scoped), scoped)
	}

	for _, id := range scoped {
		if id == "b1" || id == "b2" {
			t.Errorf("scoped query leaked team-b doc %q", id)
		}
	}

	// Continuation paging under the scope must not duplicate or drop rows: with a
	// page size of 2, the three team-a docs come back across pages exactly once.
	paged := queryIDs(ctx, t, cc, azcosmos.NewPartitionKeyString("team-a"), &azcosmos.QueryOptions{PageSizeHint: 2})
	if !sameIDSet(paged, scoped) {
		t.Errorf("paged scoped query ids=%v want same set as %v", paged, scoped)
	}

	// Cross-partition (no partition key): every document in the container.
	all := queryIDs(ctx, t, cc, azcosmos.NewPartitionKey(), nil)
	if len(all) != 5 {
		t.Errorf("cross-partition query returned %d docs (%v), want 5 (all partitions)", len(all), all)
	}
}

// sameIDSet reports whether a and b hold the same ids (order-independent, no
// duplicates expected).
func sameIDSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	seen := make(map[string]int, len(a))
	for _, id := range a {
		seen[id]++
	}

	for _, id := range b {
		seen[id]--
	}

	for _, n := range seen {
		if n != 0 {
			return false
		}
	}

	return true
}
