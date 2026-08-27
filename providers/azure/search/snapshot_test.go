package search

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/azuresearch/driver"
)

func newSnapshotTestMock() *Mock {
	opts := config.NewOptions(config.WithRegion("eastus"), config.WithAccountID("sub-1"))

	return New(opts)
}

// TestSnapshotRoundTripSearch proves a snapshot/restore round-trip preserves a
// search service, one of its indexes, and an indexed document under their
// original identities, with key fields intact.
func TestSnapshotRoundTripSearch(t *testing.T) {
	ctx := context.Background()
	src := newSnapshotTestMock()

	if _, err := src.CreateService(ctx, driver.ServiceConfig{
		Name: "svc1", ResourceGroup: "rg1", Location: "eastus", SKUName: "standard",
	}); err != nil {
		t.Fatalf("create service: %v", err)
	}

	if _, err := src.CreateOrUpdateIndex(ctx, "svc1", driver.Index{
		Name:   "idx1",
		Fields: []driver.Field{{Name: "id", Type: "Edm.String", Key: true}, {Name: "title", Type: "Edm.String"}},
	}); err != nil {
		t.Fatalf("create index: %v", err)
	}

	if _, err := src.IndexDocuments(ctx, "svc1", "idx1", []driver.IndexAction{
		{Action: "upload", Document: map[string]any{"id": "d1", "title": "hello"}},
	}); err != nil {
		t.Fatalf("index documents: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newSnapshotTestMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if svc, err := dst.GetService(ctx, "rg1", "svc1"); err != nil || svc.SKUName != "standard" {
		t.Fatalf("restored service = %+v, err %v", svc, err)
	}

	idx, err := dst.GetIndex(ctx, "svc1", "idx1")
	if err != nil || len(idx.Fields) != 2 {
		t.Fatalf("restored index = %+v, err %v", idx, err)
	}

	doc, err := dst.GetDocument(ctx, "svc1", "idx1", "d1")
	if err != nil || doc["title"] != "hello" {
		t.Fatalf("restored document = %+v, err %v", doc, err)
	}
}
