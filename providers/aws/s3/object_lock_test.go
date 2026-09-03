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

// TestObjectLockOverwriteBlocked covers overwrite protection: on a bucket where a
// write replaces the current object's bytes in place (suspended versioning), a
// protected object cannot be overwritten.
func TestObjectLockOverwriteBlocked(t *testing.T) {
	m, fc := newLockMock()
	ctx := context.Background()

	requireNoError(t, m.CreateBucket(ctx, "b"))
	requireNoError(t, m.SetVersioningStatus(ctx, "b", "Suspended"))
	requireNoError(t, m.PutObject(ctx, "b", "k", []byte("v1"), "", nil))

	requireNoError(t, m.PutObjectRetention(ctx, "b", "k", "",
		driver.ObjectRetention{Mode: driver.ObjectLockCompliance, RetainUntilDate: fc.Now().Add(time.Hour)}, false))

	// Overwriting the protected null version in place is refused.
	assertPermissionDenied(t, m.PutObject(ctx, "b", "k", []byte("v2"), "", nil))

	// The original bytes survive.
	obj, err := m.GetObject(ctx, "b", "k")
	requireNoError(t, err)
	assertEqual(t, "v1", string(obj.Data))

	// After the retention elapses, overwrite is permitted again.
	fc.Advance(2 * time.Hour)
	requireNoError(t, m.PutObject(ctx, "b", "k", []byte("v2"), "", nil))
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
