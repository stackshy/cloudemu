package persist_test

import (
	"context"
	"encoding/json"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/persist"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// TestS3StorageClassAndRestorePersist verifies an S3 object's storage class,
// system properties and RestoreObject state survive export and restore, both
// for the current object and for a noncurrent version.
func TestS3StorageClassAndRestorePersist(t *testing.T) {
	ctx := context.Background()
	src := cloudemu.NewAWS()

	if err := src.S3.CreateBucket(ctx, "archive"); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	if err := src.S3.SetVersioningStatus(ctx, "archive", "Enabled"); err != nil {
		t.Fatalf("versioning: %v", err)
	}

	glacier := &storagedriver.ObjectSystemProps{StorageClass: "GLACIER", CacheControl: "max-age=60"}
	if err := src.S3.PutObjectWithSystemProps(ctx, "archive", "old", []byte("v1"), "text/plain", nil, glacier); err != nil {
		t.Fatalf("put v1: %v", err)
	}

	v1, err := src.S3.HeadObject(ctx, "archive", "old")
	if err != nil {
		t.Fatalf("head v1: %v", err)
	}

	if _, err := src.S3.RestoreObject(ctx, "archive", "old", "", storagedriver.RestoreRequest{Days: 2}); err != nil {
		t.Fatalf("restore: %v", err)
	}

	deep := &storagedriver.ObjectSystemProps{StorageClass: "DEEP_ARCHIVE"}
	if err := src.S3.PutObjectWithSystemProps(ctx, "archive", "old", []byte("v2"), "text/plain", nil, deep); err != nil {
		t.Fatalf("put v2: %v", err)
	}

	ps, err := persist.Export(ctx, src.SnapshotServices(), persist.Options{IncludeAssets: true})
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	raw, err := json.Marshal(ps)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var state persist.ProviderState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	dst := cloudemu.NewAWS()
	if err := persist.Restore(ctx, dst.SnapshotServices(), &state); err != nil {
		t.Fatalf("restore state: %v", err)
	}

	cur, err := dst.S3.HeadObject(ctx, "archive", "old")
	if err != nil {
		t.Fatalf("head current: %v", err)
	}

	if cur.StorageClass != "DEEP_ARCHIVE" || cur.Restore != nil {
		t.Fatalf("current = class %q restore %+v, want DEEP_ARCHIVE with no restore", cur.StorageClass, cur.Restore)
	}

	if _, err := dst.S3.GetObject(ctx, "archive", "old"); err == nil {
		t.Fatal("GetObject of the unrestored DEEP_ARCHIVE version succeeded after restore")
	}

	old, err := dst.S3.GetObjectVersion(ctx, "archive", "old", v1.VersionID)
	if err != nil {
		t.Fatalf("get restored GLACIER version: %v", err)
	}

	if old.Info.StorageClass != "GLACIER" || old.Info.CacheControl != "max-age=60" || old.Info.Restore == nil {
		t.Fatalf("v1 = class %q cache %q restore %+v, want GLACIER, max-age=60, restored",
			old.Info.StorageClass, old.Info.CacheControl, old.Info.Restore)
	}

	if string(old.Data) != "v1" {
		t.Fatalf("v1 body = %q, want v1", old.Data)
	}
}
