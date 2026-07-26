package s3

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// TestVersioningSuspendedReusesNull covers #266: while Suspended, writes use the
// reserved "null" version id and overwrite the existing null version, but any
// versions created while Enabled are retained.
func TestVersioningSuspendedReusesNull(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if err := m.CreateBucket(ctx, "b"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if err := m.SetVersioningStatus(ctx, "b", "Enabled"); err != nil {
		t.Fatalf("SetVersioningStatus Enabled: %v", err)
	}
	if err := m.PutObject(ctx, "b", "k", []byte("v1"), "", nil); err != nil {
		t.Fatalf("PutObject v1: %v", err)
	}

	if err := m.SetVersioningStatus(ctx, "b", "Suspended"); err != nil {
		t.Fatalf("SetVersioningStatus Suspended: %v", err)
	}
	if err := m.PutObject(ctx, "b", "k", []byte("v2"), "", nil); err != nil {
		t.Fatalf("PutObject v2: %v", err)
	}
	if err := m.PutObject(ctx, "b", "k", []byte("v3"), "", nil); err != nil {
		t.Fatalf("PutObject v3: %v", err)
	}

	vl, err := m.ListObjectVersions(ctx, "b", driver.ListOptions{})
	if err != nil {
		t.Fatalf("ListObjectVersions: %v", err)
	}

	nullCount := 0
	for _, v := range vl.Versions {
		if v.VersionID == "null" {
			nullCount++
		}
	}
	if nullCount != 1 {
		t.Fatalf("null versions = %d, want 1 (suspended writes reuse the null version)", nullCount)
	}
	if len(vl.Versions) != 2 {
		t.Fatalf("versions = %d, want 2 (the Enabled v1 plus the single null)", len(vl.Versions))
	}

	cur, err := m.GetObject(ctx, "b", "k")
	if err != nil {
		t.Fatalf("GetObject current: %v", err)
	}
	if string(cur.Data) != "v3" || cur.Info.VersionID != "null" {
		t.Fatalf("current = %q/%q, want v3/null", cur.Data, cur.Info.VersionID)
	}
}

// TestDeleteVersionRecomputesCurrent covers #266: permanently deleting the
// latest version promotes the previous version back to current.
func TestDeleteVersionRecomputesCurrent(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if err := m.CreateBucket(ctx, "b"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if err := m.SetVersioningStatus(ctx, "b", "Enabled"); err != nil {
		t.Fatalf("SetVersioningStatus: %v", err)
	}

	if err := m.PutObject(ctx, "b", "k", []byte("v1"), "", nil); err != nil {
		t.Fatalf("PutObject v1: %v", err)
	}
	first, err := m.GetObject(ctx, "b", "k")
	if err != nil {
		t.Fatalf("GetObject v1: %v", err)
	}
	v1 := first.Info.VersionID

	if err := m.PutObject(ctx, "b", "k", []byte("v2"), "", nil); err != nil {
		t.Fatalf("PutObject v2: %v", err)
	}
	second, err := m.GetObject(ctx, "b", "k")
	if err != nil {
		t.Fatalf("GetObject v2: %v", err)
	}
	v2 := second.Info.VersionID

	if _, _, err := m.DeleteObjectVersion(ctx, "b", "k", v2); err != nil {
		t.Fatalf("DeleteObjectVersion v2: %v", err)
	}

	cur, err := m.GetObject(ctx, "b", "k")
	if err != nil {
		t.Fatalf("GetObject after deleting latest: %v", err)
	}
	if string(cur.Data) != "v1" || cur.Info.VersionID != v1 {
		t.Fatalf("current = %q/%q, want v1/%s", cur.Data, cur.Info.VersionID, v1)
	}
}
