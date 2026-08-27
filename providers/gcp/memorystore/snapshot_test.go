package memorystore

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/cache/driver"
)

// TestSnapshotRoundTripMemorystore proves the mock serializes every cache and
// its stored items and restores them into a fresh mock identity-preservingly:
// re-snapshotting yields byte-identical JSON and a value written before the
// snapshot is readable after the restore under its original cache/key.
func TestSnapshotRoundTripMemorystore(t *testing.T) {
	ctx := context.Background()
	src, _ := newTestMock()

	if _, err := src.CreateCache(ctx, driver.CacheConfig{Name: "c1", Engine: "redis"}); err != nil {
		t.Fatalf("CreateCache: %v", err)
	}

	if err := src.Set(ctx, "c1", "greeting", []byte("hello"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	data, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst, _ := newTestMock()
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	data2, err := dst.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("re-Snapshot: %v", err)
	}

	if string(data) != string(data2) {
		t.Fatalf("snapshot not stable across restore:\n%s\nvs\n%s", data, data2)
	}

	item, err := dst.Get(ctx, "c1", "greeting")
	if err != nil {
		t.Fatalf("Get after restore: %v", err)
	}

	if item == nil || string(item.Value) != "hello" {
		t.Fatalf("restored item = %+v, want value hello", item)
	}
}

// TestSnapshotEmptyMemorystore confirms a fresh mock snapshots and restores cleanly.
func TestSnapshotEmptyMemorystore(t *testing.T) {
	ctx := context.Background()
	src, _ := newTestMock()

	data, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst, _ := newTestMock()
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatalf("Restore: %v", err)
	}
}
