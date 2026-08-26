// This file exercises Cosmos optimistic concurrency (rotating _etag / ETag
// response header, If-Match preconditions mapped to 412) and partition-scoped
// query isolation, driven entirely through the real azcosmos SDK against the
// CloudEmu Azure server mounted in an httptest TLS server.

package cosmosdb_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

// TestSDKCosmosETagRotates asserts every write returns a fresh ETag (in the
// response header, surfaced as ItemResponse.ETag), a read echoes the current
// one, and the etag changes on each mutation.
func TestSDKCosmosETagRotates(t *testing.T) {
	ctx := context.Background()
	env := newCosmosEnv(t)
	cc := env.container(ctx, t, "etagdb", "docs")

	pk := azcosmos.NewPartitionKeyString("p1")

	create, _ := json.Marshal(map[string]any{"id": "d1", "pk": "p1", "rev": 1})
	createResp, err := cc.CreateItem(ctx, pk, create, nil)
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	e1 := createResp.ETag
	if e1 == "" {
		t.Fatal("CreateItem ETag empty, want a rotating etag")
	}

	readResp, err := cc.ReadItem(ctx, pk, "d1", nil)
	if err != nil {
		t.Fatalf("ReadItem: %v", err)
	}

	if readResp.ETag != e1 {
		t.Errorf("ReadItem ETag=%q want %q (read must echo the stored etag)", readResp.ETag, e1)
	}

	replace, _ := json.Marshal(map[string]any{"id": "d1", "pk": "p1", "rev": 2})
	replResp, err := cc.ReplaceItem(ctx, pk, "d1", replace, nil)
	if err != nil {
		t.Fatalf("ReplaceItem: %v", err)
	}

	if replResp.ETag == "" || replResp.ETag == e1 {
		t.Errorf("ReplaceItem ETag=%q want a new non-empty etag (was %q)", replResp.ETag, e1)
	}

	readResp2, err := cc.ReadItem(ctx, pk, "d1", nil)
	if err != nil {
		t.Fatalf("ReadItem after replace: %v", err)
	}

	if readResp2.ETag != replResp.ETag {
		t.Errorf("ReadItem ETag=%q want %q (must reflect the last write)", readResp2.ETag, replResp.ETag)
	}
}

// TestSDKCosmosIfMatchPreconditions asserts If-Match optimistic concurrency: a
// matching etag succeeds, a stale one is 412 on both replace and delete, and
// If-Match "*" matches any existing document.
func TestSDKCosmosIfMatchPreconditions(t *testing.T) {
	ctx := context.Background()
	env := newCosmosEnv(t)
	cc := env.container(ctx, t, "ifmatchdb", "docs")

	pk := azcosmos.NewPartitionKeyString("p1")

	create, _ := json.Marshal(map[string]any{"id": "d1", "pk": "p1", "rev": 1})
	createResp, err := cc.CreateItem(ctx, pk, create, nil)
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	current := createResp.ETag
	stale := azcore.ETag(`"definitely-stale"`)

	// A stale If-Match on replace is rejected 412 and does not mutate the doc.
	body2, _ := json.Marshal(map[string]any{"id": "d1", "pk": "p1", "rev": 2})
	_, staleErr := cc.ReplaceItem(ctx, pk, "d1", body2, &azcosmos.ItemOptions{IfMatchEtag: &stale})
	wantRespErr(t, staleErr, 412, "ReplaceItem stale If-Match")

	if got := readDoc(ctx, t, cc, "p1", "d1"); got["rev"] != float64(1) {
		t.Errorf("rev=%v want 1 (stale replace must not apply)", got["rev"])
	}

	// A matching If-Match on replace succeeds and rotates the etag.
	matchResp, err := cc.ReplaceItem(ctx, pk, "d1", body2, &azcosmos.ItemOptions{IfMatchEtag: &current})
	if err != nil {
		t.Fatalf("ReplaceItem matching If-Match: %v", err)
	}

	newEtag := matchResp.ETag
	if newEtag == current {
		t.Errorf("etag=%q did not rotate after matching-If-Match replace", newEtag)
	}

	// A stale If-Match on delete is rejected 412; the doc survives.
	_, delStaleErr := cc.DeleteItem(ctx, pk, "d1", &azcosmos.ItemOptions{IfMatchEtag: &current})
	wantRespErr(t, delStaleErr, 412, "DeleteItem stale If-Match")

	if got := readDoc(ctx, t, cc, "p1", "d1"); got["rev"] != float64(2) {
		t.Errorf("rev=%v want 2 (stale-If-Match delete must not remove)", got["rev"])
	}

	// If-Match "*" matches any existing document, so the delete succeeds.
	star := azcore.ETag("*")
	if _, err := cc.DeleteItem(ctx, pk, "d1", &azcosmos.ItemOptions{IfMatchEtag: &star}); err != nil {
		t.Fatalf("DeleteItem If-Match *: %v", err)
	}

	_, err = cc.ReadItem(ctx, pk, "d1", nil)
	wantRespErr(t, err, 404, "ReadItem after If-Match * delete")
}
