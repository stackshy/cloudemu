package s3

import (
	"context"
	"errors"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// TestCopyObjectV2MetadataReplace verifies REPLACE swaps metadata + content
// type while COPY (default) carries the source's.
func TestCopyObjectV2MetadataReplace(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if err := m.CreateBucket(ctx, "b"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	if err := m.PutObject(ctx, "b", "src", []byte("data"), "text/plain", map[string]string{"k": "src"}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	res, err := m.CopyObjectV2(ctx, &driver.CopyObjectRequest{
		DstBucket: "b", DstKey: "replaced", Src: driver.CopySource{Bucket: "b", Key: "src"},
		ReplaceMetadata: true, Metadata: map[string]string{"k": "new"}, ContentType: "application/json",
	})
	if err != nil {
		t.Fatalf("CopyObjectV2 REPLACE: %v", err)
	}

	if res.ETag == "" {
		t.Fatal("CopyObjectV2 returned empty ETag")
	}

	info, err := m.HeadObject(ctx, "b", "replaced")
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}

	if info.Metadata["k"] != "new" || info.ContentType != "application/json" {
		t.Fatalf("REPLACE mismatch: meta=%v ct=%q", info.Metadata, info.ContentType)
	}
}

// TestCopyObjectV2Preconditions verifies a failed copy-source precondition is a
// FailedPrecondition error and leaves the destination untouched.
func TestCopyObjectV2Preconditions(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if err := m.CreateBucket(ctx, "b"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	if err := m.PutObject(ctx, "b", "src", []byte("data"), "text/plain", nil); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	info, err := m.HeadObject(ctx, "b", "src")
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}

	_, err = m.CopyObjectV2(ctx, &driver.CopyObjectRequest{
		DstBucket: "b", DstKey: "dst", Src: driver.CopySource{Bucket: "b", Key: "src"},
		IfMatch: "deadbeef",
	})
	if !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("if-match mismatch err = %v, want FailedPrecondition", err)
	}

	if _, err := m.HeadObject(ctx, "b", "dst"); !cerrors.IsNotFound(err) {
		t.Fatal("destination created despite failed precondition")
	}

	// A matching if-match copies.
	if _, err := m.CopyObjectV2(ctx, &driver.CopyObjectRequest{
		DstBucket: "b", DstKey: "dst", Src: driver.CopySource{Bucket: "b", Key: "src"},
		IfMatch: info.ETag,
	}); err != nil {
		t.Fatalf("CopyObjectV2 matching if-match: %v", err)
	}
}

// TestCopyObjectV2VersionedSource copies a specific older version of a source
// object on a versioning-enabled bucket.
func TestCopyObjectV2VersionedSource(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if err := m.CreateBucket(ctx, "b"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	if err := m.SetVersioningStatus(ctx, "b", versioningEnabled); err != nil {
		t.Fatalf("SetVersioningStatus: %v", err)
	}

	if err := m.PutObject(ctx, "b", "k", []byte("one"), "text/plain", nil); err != nil {
		t.Fatalf("PutObject v1: %v", err)
	}

	v1, err := m.HeadObject(ctx, "b", "k")
	if err != nil {
		t.Fatalf("HeadObject v1: %v", err)
	}

	oldVersion := v1.VersionID

	if err := m.PutObject(ctx, "b", "k", []byte("two"), "text/plain", nil); err != nil {
		t.Fatalf("PutObject v2: %v", err)
	}

	res, err := m.CopyObjectV2(ctx, &driver.CopyObjectRequest{
		DstBucket: "b", DstKey: "restored", Src: driver.CopySource{Bucket: "b", Key: "k"},
		SrcVersionID: oldVersion,
	})
	if err != nil {
		t.Fatalf("CopyObjectV2 versioned source: %v", err)
	}

	if res.SourceVersionID != oldVersion {
		t.Fatalf("SourceVersionID = %q, want %q", res.SourceVersionID, oldVersion)
	}

	obj, err := m.GetObject(ctx, "b", "restored")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}

	if string(obj.Data) != "one" {
		t.Fatalf("copied body = %q, want 'one'", string(obj.Data))
	}
}

// TestGetObjectVersionDeleteMarker verifies a delete-marker version yields
// driver.ErrDeleteMarker (so the wire layer can answer 405), while a genuinely
// missing version stays NotFound.
func TestGetObjectVersionDeleteMarker(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if err := m.CreateBucket(ctx, "b"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	if err := m.SetVersioningStatus(ctx, "b", versioningEnabled); err != nil {
		t.Fatalf("SetVersioningStatus: %v", err)
	}

	if err := m.PutObject(ctx, "b", "k", []byte("v1"), "text/plain", nil); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	markerID, marker, err := m.DeleteObjectVersion(ctx, "b", "k", "")
	if err != nil || !marker {
		t.Fatalf("DeleteObjectVersion delete-marker: id=%q marker=%v err=%v", markerID, marker, err)
	}

	if _, err := m.GetObjectVersion(ctx, "b", "k", markerID); !errors.Is(err, driver.ErrDeleteMarker) {
		t.Fatalf("GetObjectVersion(delete marker) err = %v, want ErrDeleteMarker", err)
	}

	if _, err := m.HeadObjectVersion(ctx, "b", "k", markerID); !errors.Is(err, driver.ErrDeleteMarker) {
		t.Fatalf("HeadObjectVersion(delete marker) err = %v, want ErrDeleteMarker", err)
	}

	if _, err := m.GetObjectVersion(ctx, "b", "k", "no-such-version"); !cerrors.IsNotFound(err) ||
		errors.Is(err, driver.ErrDeleteMarker) {
		t.Fatalf("GetObjectVersion(missing) err = %v, want plain NotFound", err)
	}
}

// TestCreateBucketInRegion records a caller-specified region so
// GetBucketLocation can report it, and falls back to the default otherwise.
func TestCreateBucketInRegion(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if err := m.CreateBucketInRegion(ctx, "west", "us-west-2"); err != nil {
		t.Fatalf("CreateBucketInRegion: %v", err)
	}

	if err := m.CreateBucket(ctx, "east"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	buckets, err := m.ListBuckets(ctx)
	if err != nil {
		t.Fatalf("ListBuckets: %v", err)
	}

	regions := map[string]string{}
	for _, b := range buckets {
		regions[b.Name] = b.Region
	}

	if regions["west"] != "us-west-2" {
		t.Fatalf("west region = %q, want us-west-2", regions["west"])
	}

	if regions["east"] != "us-east-1" {
		t.Fatalf("east region = %q, want us-east-1 (default)", regions["east"])
	}
}
