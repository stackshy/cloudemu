package dns

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	driver "github.com/stackshy/cloudemu/v2/services/dns/driver"
)

// TestRestoreKeepsETagSequence checks that a record set written after a
// snapshot restore gets an etag it never had before, so an If-Match carrying
// an etag from before the restore still fails.
func TestRestoreKeepsETagSequence(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	zone, err := src.CreateZone(ctx, driver.ZoneConfig{Name: "example.com"})
	if err != nil {
		t.Fatalf("create zone: %v", err)
	}

	cfg := driver.RecordConfig{ZoneID: zone.ID, Name: "www", Type: "A", TTL: 300, Values: []string{"192.0.2.1"}}

	first, _, err := src.UpsertRecordAtomic(ctx, cfg, "", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	cfg.TTL = 600

	second, _, err := src.UpsertRecordAtomic(ctx, cfg, first.ETag, "")
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	data, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatalf("restore: %v", err)
	}

	cfg.TTL = 900

	third, _, err := dst.UpsertRecordAtomic(ctx, cfg, second.ETag, "")
	if err != nil {
		t.Fatalf("update after restore: %v", err)
	}

	if third.ETag == first.ETag || third.ETag == second.ETag {
		t.Fatalf("etag %q repeats one from before the restore", third.ETag)
	}

	cfg.TTL = 1200

	if _, _, err := dst.UpsertRecordAtomic(ctx, cfg, first.ETag, ""); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("stale If-Match after restore: err=%v, want FailedPrecondition", err)
	}
}
