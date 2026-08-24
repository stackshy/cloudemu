package tablestorage_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/data/aztables"
)

// TestGetEntitySystemProperties covers finding #3: a fetched entity carries the
// system Timestamp and odata.etag properties, and the ETag response header.
func TestGetEntitySystemProperties(t *testing.T) {
	ctx := context.Background()
	client, _ := newTableClient(t, "sysprops")
	addEntity(t, client, "p", "r", map[string]any{"X": 1})

	got, err := client.GetEntity(ctx, "p", "r", nil)
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}

	if got.ETag == "" {
		t.Error("GetEntity response has no ETag header")
	}

	var props map[string]any
	if err := json.Unmarshal(got.Value, &props); err != nil {
		t.Fatalf("unmarshal entity: %v", err)
	}

	if props["Timestamp"] == nil {
		t.Error("entity body missing Timestamp system property")
	}

	if props["odata.etag"] == nil {
		t.Error("entity body missing odata.etag system property")
	}
}

// TestETagStableAndConditionalUpdate covers finding #8: the ETag is stable
// across unmodified reads, and a stale If-Match update is rejected with 412.
func TestETagStableAndConditionalUpdate(t *testing.T) {
	ctx := context.Background()
	client, _ := newTableClient(t, "etag")
	addEntity(t, client, "p", "r", map[string]any{"V": 1})

	first, err := client.GetEntity(ctx, "p", "r", nil)
	if err != nil {
		t.Fatalf("first GetEntity: %v", err)
	}

	second, err := client.GetEntity(ctx, "p", "r", nil)
	if err != nil {
		t.Fatalf("second GetEntity: %v", err)
	}

	if first.ETag != second.ETag {
		t.Fatalf("ETag not stable: %q vs %q", first.ETag, second.ETag)
	}

	original := first.ETag

	// A conditional replace with the current ETag succeeds and rotates the ETag.
	body := marshalEntity(t, "p", "r", map[string]any{"V": 2})

	if _, err := client.UpdateEntity(ctx, body, &aztables.UpdateEntityOptions{
		IfMatch:    to.Ptr(original),
		UpdateMode: aztables.UpdateModeReplace,
	}); err != nil {
		t.Fatalf("conditional UpdateEntity with current ETag: %v", err)
	}

	// A second replace with the now-stale original ETag must fail with 412.
	_, err = client.UpdateEntity(ctx, body, &aztables.UpdateEntityOptions{
		IfMatch:    to.Ptr(original),
		UpdateMode: aztables.UpdateModeReplace,
	})
	if err == nil {
		t.Fatal("stale conditional update succeeded, want 412 UpdateConditionNotSatisfied")
	}

	if !strings.Contains(err.Error(), "UpdateConditionNotSatisfied") {
		t.Errorf("stale update error = %v, want UpdateConditionNotSatisfied", err)
	}
}

// TestQueryTopPagination covers finding #7: $top caps the page and a
// continuation token lets the pager walk the whole result set exactly once.
func TestQueryTopPagination(t *testing.T) {
	ctx := context.Background()
	client, _ := newTableClient(t, "paging")

	for _, rk := range []string{"a", "b", "c"} {
		addEntity(t, client, "p", rk, nil)
	}

	pager := client.NewListEntitiesPager(&aztables.ListEntitiesOptions{Top: to.Ptr(int32(1))})

	pages := 0
	seen := map[string]bool{}

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		pages++

		if len(page.Entities) > 1 {
			t.Fatalf("page %d returned %d entities, want at most 1 (Top=1)", pages, len(page.Entities))
		}

		for _, raw := range page.Entities {
			var e map[string]any
			if err := json.Unmarshal(raw, &e); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			rk, _ := e["RowKey"].(string)
			if seen[rk] {
				t.Fatalf("row %q returned twice across pages", rk)
			}

			seen[rk] = true
		}
	}

	if len(seen) != 3 {
		t.Fatalf("paged rows = %v, want a,b,c", seen)
	}

	if pages < 3 {
		t.Fatalf("walked %d pages for 3 rows with Top=1, want >= 3 (no continuation?)", pages)
	}
}
