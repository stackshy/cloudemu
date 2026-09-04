package s3

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

func newLockMock() (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return New(opts), fc
}

// currentVersionID returns the version id of key's current object.
func currentVersionID(t *testing.T, m *Mock, bucket, key string) string {
	t.Helper()

	obj, err := m.GetObject(context.Background(), bucket, key)
	requireNoError(t, err)

	return obj.Info.VersionID
}

func assertPermissionDenied(t *testing.T, err error) {
	t.Helper()

	if !cerrors.IsPermissionDenied(err) {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
}

// TestObjectLockComplianceBlocksDelete covers a COMPLIANCE retention: the version
// cannot be permanently deleted (even with governance bypass) until the retain
// date elapses, after which the delete succeeds.
func TestObjectLockComplianceBlocksDelete(t *testing.T) {
	m, fc := newLockMock()
	ctx := context.Background()

	requireNoError(t, m.CreateBucket(ctx, "b"))
	requireNoError(t, m.EnableObjectLock(ctx, "b"))
	requireNoError(t, m.PutObject(ctx, "b", "k", []byte("v1"), "", nil))

	vid := currentVersionID(t, m, "b", "k")
	until := fc.Now().Add(time.Hour)

	requireNoError(t, m.PutObjectRetention(ctx, "b", "k", vid,
		driver.ObjectRetention{Mode: driver.ObjectLockCompliance, RetainUntilDate: until}, false))

	_, _, err := m.DeleteObjectVersion(ctx, "b", "k", vid)
	assertPermissionDenied(t, err)

	// COMPLIANCE cannot be bypassed by anyone.
	_, _, err = m.DeleteObjectVersionWithBypass(ctx, "b", "k", vid, true)
	assertPermissionDenied(t, err)

	// After the retain date, the delete is permitted.
	fc.Advance(2 * time.Hour)

	_, _, err = m.DeleteObjectVersion(ctx, "b", "k", vid)
	requireNoError(t, err)
}

// TestObjectLockGovernanceBypass covers a GOVERNANCE retention: blocked without
// bypass, permitted with x-amz-bypass-governance-retention.
func TestObjectLockGovernanceBypass(t *testing.T) {
	m, fc := newLockMock()
	ctx := context.Background()

	requireNoError(t, m.CreateBucket(ctx, "b"))
	requireNoError(t, m.EnableObjectLock(ctx, "b"))
	requireNoError(t, m.PutObject(ctx, "b", "k", []byte("v1"), "", nil))

	vid := currentVersionID(t, m, "b", "k")

	requireNoError(t, m.PutObjectRetention(ctx, "b", "k", vid,
		driver.ObjectRetention{Mode: driver.ObjectLockGovernance, RetainUntilDate: fc.Now().Add(time.Hour)}, false))

	_, _, err := m.DeleteObjectVersion(ctx, "b", "k", vid)
	assertPermissionDenied(t, err)

	_, _, err = m.DeleteObjectVersionWithBypass(ctx, "b", "k", vid, true)
	requireNoError(t, err)
}

// TestObjectLockLegalHoldBlocksDelete covers legal hold: it blocks a delete
// regardless of retention (and cannot be bypassed) until turned OFF.
func TestObjectLockLegalHoldBlocksDelete(t *testing.T) {
	m, _ := newLockMock()
	ctx := context.Background()

	requireNoError(t, m.CreateBucket(ctx, "b"))
	requireNoError(t, m.EnableObjectLock(ctx, "b"))
	requireNoError(t, m.PutObject(ctx, "b", "k", []byte("v1"), "", nil))

	vid := currentVersionID(t, m, "b", "k")

	requireNoError(t, m.PutObjectLegalHold(ctx, "b", "k", vid, true))

	on, err := m.GetObjectLegalHold(ctx, "b", "k", vid)
	requireNoError(t, err)
	assertEqual(t, true, on)

	// No retention set, yet the delete is blocked by legal hold — even with bypass.
	_, _, err = m.DeleteObjectVersionWithBypass(ctx, "b", "k", vid, true)
	assertPermissionDenied(t, err)

	requireNoError(t, m.PutObjectLegalHold(ctx, "b", "k", vid, false))

	_, _, err = m.DeleteObjectVersion(ctx, "b", "k", vid)
	requireNoError(t, err)
}

// setObjectLockForTest stamps lock state directly on a stored object and its
// current version. Real S3 only allows Object Lock on a versioning-enabled
// (object-lock-enabled) bucket, so this white-box helper is the only way to
// construct a locked object on an unversioned/suspended bucket — the exact state
// the in-place-overwrite and top-level-delete WORM guards defend against (e.g.
// after a snapshot/restore of a crafted state).
func setObjectLockForTest(t *testing.T, m *Mock, bucket, key string, l objectLock) {
	t.Helper()

	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		t.Fatalf("bucket %q not found", bucket)
	}

	bkt.versionsMu.Lock()
	defer bkt.versionsMu.Unlock()

	if obj, has := bkt.objects.Get(key); has {
		obj.lock = l
	}

	if v := currentVersionLocked(bkt, key); v != nil {
		v.lock = l
	}
}

// TestObjectLockOverwriteBlocked covers overwrite protection: on a bucket where a
// write replaces the current object's bytes in place (unversioned), a protected
// object cannot be overwritten.
func TestObjectLockOverwriteBlocked(t *testing.T) {
	m, fc := newLockMock()
	ctx := context.Background()

	requireNoError(t, m.CreateBucket(ctx, "b"))
	requireNoError(t, m.PutObject(ctx, "b", "k", []byte("v1"), "", nil))

	setObjectLockForTest(t, m, "b", "k",
		objectLock{retentionMode: driver.ObjectLockCompliance, retainUntil: fc.Now().Add(time.Hour)})

	// Overwriting the protected object in place is refused.
	assertPermissionDenied(t, m.PutObject(ctx, "b", "k", []byte("v2"), "", nil))

	// The original bytes survive.
	obj, err := m.GetObject(ctx, "b", "k")
	requireNoError(t, err)
	assertEqual(t, "v1", string(obj.Data))

	// After the retention elapses, overwrite is permitted again.
	fc.Advance(2 * time.Hour)
	requireNoError(t, m.PutObject(ctx, "b", "k", []byte("v2"), "", nil))
}

// TestObjectLockTopLevelDeleteBlockedUnversioned covers the WORM guard on a
// top-level (no versionId) delete of an in-place object: it must not destroy a
// COMPLIANCE-locked or legal-held object's bytes. (Defense-in-depth — real S3
// cannot reach this state, so the lock is crafted white-box.)
func TestObjectLockTopLevelDeleteBlockedUnversioned(t *testing.T) {
	m, fc := newLockMock()
	ctx := context.Background()

	requireNoError(t, m.CreateBucket(ctx, "b"))

	// COMPLIANCE retention: top-level delete blocked, even with governance bypass.
	requireNoError(t, m.PutObject(ctx, "b", "comp", []byte("v1"), "", nil))
	setObjectLockForTest(t, m, "b", "comp",
		objectLock{retentionMode: driver.ObjectLockCompliance, retainUntil: fc.Now().Add(time.Hour)})

	assertPermissionDenied(t, m.DeleteObject(ctx, "b", "comp"))
	_, _, err := m.DeleteObjectVersionWithBypass(ctx, "b", "comp", "", true)
	assertPermissionDenied(t, err)

	got, err := m.GetObject(ctx, "b", "comp")
	requireNoError(t, err)
	assertEqual(t, "v1", string(got.Data))

	// Legal hold: top-level delete blocked, cannot be bypassed.
	requireNoError(t, m.PutObject(ctx, "b", "lh", []byte("v1"), "", nil))
	setObjectLockForTest(t, m, "b", "lh", objectLock{legalHold: true})

	assertPermissionDenied(t, m.DeleteObject(ctx, "b", "lh"))
	_, _, err = m.DeleteObjectVersionWithBypass(ctx, "b", "lh", "", true)
	assertPermissionDenied(t, err)

	// After the COMPLIANCE retention elapses, the delete is permitted.
	fc.Advance(2 * time.Hour)
	requireNoError(t, m.DeleteObject(ctx, "b", "comp"))
}

// TestObjectLockTopLevelDeleteBlockedSuspended covers the WORM guard on a
// top-level delete against the versioning-suspended null-version branch.
func TestObjectLockTopLevelDeleteBlockedSuspended(t *testing.T) {
	m, fc := newLockMock()
	ctx := context.Background()

	requireNoError(t, m.CreateBucket(ctx, "b"))
	requireNoError(t, m.SetVersioningStatus(ctx, "b", "Suspended"))
	requireNoError(t, m.PutObject(ctx, "b", "k", []byte("v1"), "", nil))

	setObjectLockForTest(t, m, "b", "k",
		objectLock{retentionMode: driver.ObjectLockCompliance, retainUntil: fc.Now().Add(time.Hour)})

	// Top-level delete would replace the null version with a null delete marker,
	// destroying the protected bytes — it must be refused.
	assertPermissionDenied(t, m.DeleteObject(ctx, "b", "k"))

	got, err := m.GetObject(ctx, "b", "k")
	requireNoError(t, err)
	assertEqual(t, "v1", string(got.Data))
}

// TestObjectLockSuspendRejected covers that versioning cannot be suspended on an
// Object-Lock-enabled bucket.
func TestObjectLockSuspendRejected(t *testing.T) {
	m, _ := newLockMock()
	ctx := context.Background()

	requireNoError(t, m.CreateBucket(ctx, "b"))
	requireNoError(t, m.EnableObjectLock(ctx, "b"))

	if err := m.SetVersioningStatus(ctx, "b", "Suspended"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("SetVersioningStatus Suspended = %v, want FailedPrecondition", err)
	}

	// Re-enabling is fine.
	requireNoError(t, m.SetVersioningStatus(ctx, "b", "Enabled"))
}

// TestObjectLockRetentionRequiresLockBucket covers that retention/legal hold can
// only be set on an Object-Lock-enabled bucket.
func TestObjectLockRetentionRequiresLockBucket(t *testing.T) {
	m, fc := newLockMock()
	ctx := context.Background()

	requireNoError(t, m.CreateBucket(ctx, "plain"))
	requireNoError(t, m.PutObject(ctx, "plain", "k", []byte("v1"), "", nil))

	err := m.PutObjectRetention(ctx, "plain", "k", "",
		driver.ObjectRetention{Mode: driver.ObjectLockCompliance, RetainUntilDate: fc.Now().Add(time.Hour)}, false)
	if !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("PutObjectRetention on non-lock bucket = %v, want FailedPrecondition", err)
	}

	if err := m.PutObjectLegalHold(ctx, "plain", "k", "", true); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("PutObjectLegalHold on non-lock bucket = %v, want FailedPrecondition", err)
	}
}

// TestObjectLockEnabledOverwriteMakesNewVersion covers that on an object-lock
// (versioning-enabled) bucket a PUT is a fresh version and never destroys the
// protected version's bytes, so it is always allowed.
func TestObjectLockEnabledOverwriteMakesNewVersion(t *testing.T) {
	m, fc := newLockMock()
	ctx := context.Background()

	requireNoError(t, m.CreateBucket(ctx, "b"))
	requireNoError(t, m.EnableObjectLock(ctx, "b"))
	requireNoError(t, m.PutObject(ctx, "b", "k", []byte("v1"), "", nil))

	v1 := currentVersionID(t, m, "b", "k")
	requireNoError(t, m.PutObjectRetention(ctx, "b", "k", v1,
		driver.ObjectRetention{Mode: driver.ObjectLockCompliance, RetainUntilDate: fc.Now().Add(time.Hour)}, false))

	// A new PUT succeeds (new version); the locked v1 is preserved.
	requireNoError(t, m.PutObject(ctx, "b", "k", []byte("v2"), "", nil))

	vl, err := m.ListObjectVersions(ctx, "b", driver.ListOptions{})
	requireNoError(t, err)
	assertEqual(t, 2, len(vl.Versions))

	// The locked v1 still cannot be deleted.
	_, _, err = m.DeleteObjectVersion(ctx, "b", "k", v1)
	assertPermissionDenied(t, err)
}

// TestObjectLockTopLevelDeleteMarker covers that a top-level delete (no version
// id) records a delete marker and leaves the protected version intact.
func TestObjectLockTopLevelDeleteMarker(t *testing.T) {
	m, fc := newLockMock()
	ctx := context.Background()

	requireNoError(t, m.CreateBucket(ctx, "b"))
	requireNoError(t, m.EnableObjectLock(ctx, "b"))
	requireNoError(t, m.PutObject(ctx, "b", "k", []byte("v1"), "", nil))

	v1 := currentVersionID(t, m, "b", "k")
	requireNoError(t, m.PutObjectRetention(ctx, "b", "k", v1,
		driver.ObjectRetention{Mode: driver.ObjectLockCompliance, RetainUntilDate: fc.Now().Add(time.Hour)}, false))

	vid, marker, err := m.DeleteObjectVersion(ctx, "b", "k", "")
	requireNoError(t, err)
	assertEqual(t, true, marker)
	if vid == "" {
		t.Fatal("expected a delete-marker version id")
	}

	// The protected version is still present in the history.
	if findVersion := (func() bool {
		vl, lErr := m.ListObjectVersions(ctx, "b", driver.ListOptions{})
		requireNoError(t, lErr)
		for _, v := range vl.Versions {
			if v.VersionID == v1 {
				return true
			}
		}
		return false
	})(); !findVersion {
		t.Fatal("protected version was removed by a top-level delete")
	}
}

// TestObjectLockRetentionModificationRules covers the retention change rules:
// COMPLIANCE can only be extended; GOVERNANCE can be shortened only with bypass.
func TestObjectLockRetentionModificationRules(t *testing.T) {
	m, fc := newLockMock()
	ctx := context.Background()

	requireNoError(t, m.CreateBucket(ctx, "b"))
	requireNoError(t, m.EnableObjectLock(ctx, "b"))

	requireNoError(t, m.PutObject(ctx, "b", "comp", []byte("v"), "", nil))
	compVID := currentVersionID(t, m, "b", "comp")
	base := fc.Now().Add(time.Hour)
	requireNoError(t, m.PutObjectRetention(ctx, "b", "comp", compVID,
		driver.ObjectRetention{Mode: driver.ObjectLockCompliance, RetainUntilDate: base}, false))

	// Extending COMPLIANCE is allowed.
	requireNoError(t, m.PutObjectRetention(ctx, "b", "comp", compVID,
		driver.ObjectRetention{Mode: driver.ObjectLockCompliance, RetainUntilDate: base.Add(time.Hour)}, false))

	// Shortening COMPLIANCE is refused, even with bypass.
	assertPermissionDenied(t, m.PutObjectRetention(ctx, "b", "comp", compVID,
		driver.ObjectRetention{Mode: driver.ObjectLockCompliance, RetainUntilDate: base}, true))

	requireNoError(t, m.PutObject(ctx, "b", "gov", []byte("v"), "", nil))
	govVID := currentVersionID(t, m, "b", "gov")
	requireNoError(t, m.PutObjectRetention(ctx, "b", "gov", govVID,
		driver.ObjectRetention{Mode: driver.ObjectLockGovernance, RetainUntilDate: base.Add(time.Hour)}, false))

	// Shortening GOVERNANCE without bypass is refused; with bypass it succeeds.
	assertPermissionDenied(t, m.PutObjectRetention(ctx, "b", "gov", govVID,
		driver.ObjectRetention{Mode: driver.ObjectLockGovernance, RetainUntilDate: base}, false))
	requireNoError(t, m.PutObjectRetention(ctx, "b", "gov", govVID,
		driver.ObjectRetention{Mode: driver.ObjectLockGovernance, RetainUntilDate: base}, true))
}

// TestObjectLockRetentionSnapshotRoundTrip covers that retention/legal-hold
// survive a Snapshot/Restore cycle.
func TestObjectLockRetentionSnapshotRoundTrip(t *testing.T) {
	m, fc := newLockMock()
	ctx := context.Background()

	requireNoError(t, m.CreateBucket(ctx, "b"))
	requireNoError(t, m.EnableObjectLock(ctx, "b"))
	requireNoError(t, m.PutObject(ctx, "b", "k", []byte("v1"), "", nil))

	vid := currentVersionID(t, m, "b", "k")
	until := fc.Now().Add(time.Hour)
	requireNoError(t, m.PutObjectRetention(ctx, "b", "k", vid,
		driver.ObjectRetention{Mode: driver.ObjectLockCompliance, RetainUntilDate: until}, false))

	snap, err := m.Snapshot(ctx, true)
	requireNoError(t, err)

	restored, _ := newLockMock()
	requireNoError(t, restored.Restore(ctx, snap))

	enabled, err := restored.ObjectLockEnabled(ctx, "b")
	requireNoError(t, err)
	assertEqual(t, true, enabled)

	ret, err := restored.GetObjectRetention(ctx, "b", "k", vid)
	requireNoError(t, err)
	assertEqual(t, driver.ObjectLockCompliance, ret.Mode)

	// The restored version is still protected.
	_, _, err = restored.DeleteObjectVersion(ctx, "b", "k", vid)
	assertPermissionDenied(t, err)
}
