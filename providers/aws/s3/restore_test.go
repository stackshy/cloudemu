package s3

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// restoreBase is 10:30 UTC so expiry rounding to the next midnight is visible.
var restoreBase = time.Date(2025, 10, 15, 10, 30, 0, 0, time.UTC) //nolint:gochecknoglobals // test fixture

// newRestoreMock returns a mock on a fake clock, with async settling when
// settleOn is set, and a bucket "b".
func newRestoreMock(t *testing.T, settleOn bool) (*Mock, *config.FakeClock) {
	t.Helper()

	fc := config.NewFakeClock(restoreBase)
	opts := []config.Option{config.WithClock(fc), config.WithRegion("us-east-1")}

	if settleOn {
		opts = append(opts, config.WithAsyncSettle())
	}

	m := New(config.NewOptions(opts...))
	requireNoError(t, m.CreateBucket(context.Background(), "b"))

	return m, fc
}

// putClass stores key with storage class sc.
func putClass(t *testing.T, m *Mock, key, sc string) {
	t.Helper()

	requireNoError(t, m.PutObjectWithSystemProps(context.Background(), "b", key, []byte("data"), "text/plain", nil,
		&driver.ObjectSystemProps{StorageClass: sc}))
}

func assertInvalidObjectState(t *testing.T, err error, sc string) {
	t.Helper()

	var se *driver.InvalidObjectStateError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v (%T), want *driver.InvalidObjectStateError", err, err)
	}

	assertEqual(t, sc, se.StorageClass)

	if !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("InvalidObjectState does not read as FailedPrecondition: %v", err)
	}
}

func TestRestoreLifecycleWithSettle(t *testing.T) {
	ctx := context.Background()
	m, fc := newRestoreMock(t, true)

	putClass(t, m, "k", storageClassGlacier)

	_, err := m.GetObject(ctx, "b", "k")
	assertInvalidObjectState(t, err, storageClassGlacier)

	info, err := m.HeadObject(ctx, "b", "k")
	requireNoError(t, err)

	if info.Restore != nil {
		t.Fatalf("Restore before any request = %+v, want nil", info.Restore)
	}

	accepted, err := m.RestoreObject(ctx, "b", "k", "", driver.RestoreRequest{Days: 3})
	requireNoError(t, err)
	assertEqual(t, true, accepted)

	info, err = m.HeadObject(ctx, "b", "k")
	requireNoError(t, err)

	if info.Restore == nil || !info.Restore.InProgress {
		t.Fatalf("Restore while running = %+v, want in progress", info.Restore)
	}

	_, err = m.GetObject(ctx, "b", "k")
	assertInvalidObjectState(t, err, storageClassGlacier)

	_, err = m.RestoreObject(ctx, "b", "k", "", driver.RestoreRequest{Days: 5})
	if !errors.Is(err, driver.ErrRestoreAlreadyInProgress) {
		t.Fatalf("restore while running err = %v, want ErrRestoreAlreadyInProgress", err)
	}

	fc.Advance(settle.DefaultRestoreStandardSettle)

	info, err = m.HeadObject(ctx, "b", "k")
	requireNoError(t, err)

	// Completed 10:30:03 on Oct 15 + 3 days, rounded up to Oct 19 00:00 UTC.
	want := time.Date(2025, 10, 19, 0, 0, 0, 0, time.UTC)
	if info.Restore == nil || info.Restore.InProgress || !info.Restore.ExpiryDate.Equal(want) {
		t.Fatalf("Restore after settle = %+v, want expiry %v", info.Restore, want)
	}

	assertEqual(t, storageClassGlacier, info.StorageClass)

	obj, err := m.GetObject(ctx, "b", "k")
	requireNoError(t, err)
	assertEqual(t, "data", string(obj.Data))

	// Re-issuing after completion resets the expiry relative to now (200).
	fc.Advance(24 * time.Hour)

	accepted, err = m.RestoreObject(ctx, "b", "k", "", driver.RestoreRequest{Days: 10})
	requireNoError(t, err)
	assertEqual(t, false, accepted)

	info, err = m.HeadObject(ctx, "b", "k")
	requireNoError(t, err)

	want = time.Date(2025, 10, 27, 0, 0, 0, 0, time.UTC)
	if info.Restore == nil || !info.Restore.ExpiryDate.Equal(want) {
		t.Fatalf("Restore after extend = %+v, want expiry %v", info.Restore, want)
	}

	// Past the expiry the copy is gone: the object is archived again and a new
	// restore is a fresh one (202).
	fc.Advance(15 * 24 * time.Hour)

	_, err = m.GetObject(ctx, "b", "k")
	assertInvalidObjectState(t, err, storageClassGlacier)

	info, err = m.HeadObject(ctx, "b", "k")
	requireNoError(t, err)

	if info.Restore != nil {
		t.Fatalf("Restore after expiry = %+v, want nil", info.Restore)
	}

	accepted, err = m.RestoreObject(ctx, "b", "k", "", driver.RestoreRequest{Days: 1, Tier: driver.RestoreTierBulk})
	requireNoError(t, err)
	assertEqual(t, true, accepted)
}

func TestRestoreCompletesAtOnceWithoutSettle(t *testing.T) {
	ctx := context.Background()
	m, _ := newRestoreMock(t, false)

	putClass(t, m, "k", storageClassDeepArchive)

	accepted, err := m.RestoreObject(ctx, "b", "k", "", driver.RestoreRequest{Days: 1})
	requireNoError(t, err)
	assertEqual(t, true, accepted)

	obj, err := m.GetObject(ctx, "b", "k")
	requireNoError(t, err)

	if obj.Info.Restore == nil || obj.Info.Restore.InProgress {
		t.Fatalf("Restore = %+v, want completed", obj.Info.Restore)
	}
}

func TestRestoreValidation(t *testing.T) {
	ctx := context.Background()
	m, _ := newRestoreMock(t, false)

	putClass(t, m, "std", "")
	putClass(t, m, "ia", "STANDARD_IA")
	putClass(t, m, "it", storageClassIntelligentTiering)
	putClass(t, m, "glacier", storageClassGlacier)
	putClass(t, m, "deep", storageClassDeepArchive)

	_, err := m.RestoreObject(ctx, "b", "std", "", driver.RestoreRequest{Days: 1})
	assertInvalidObjectState(t, err, "STANDARD")

	_, err = m.RestoreObject(ctx, "b", "ia", "", driver.RestoreRequest{Days: 1})
	assertInvalidObjectState(t, err, "STANDARD_IA")

	_, err = m.RestoreObject(ctx, "b", "it", "", driver.RestoreRequest{})
	if !errors.Is(err, driver.ErrObjectAlreadyInActiveTier) {
		t.Fatalf("IT restore err = %v, want ErrObjectAlreadyInActiveTier", err)
	}

	_, err = m.RestoreObject(ctx, "b", "it", "", driver.RestoreRequest{Days: 1})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("IT restore with Days err = %v, want InvalidArgument", err)
	}

	_, err = m.RestoreObject(ctx, "b", "glacier", "", driver.RestoreRequest{})
	if !errors.Is(err, driver.ErrRestoreDaysRequired) {
		t.Fatalf("restore without Days err = %v, want ErrRestoreDaysRequired", err)
	}

	_, err = m.RestoreObject(ctx, "b", "deep", "", driver.RestoreRequest{Days: 1, Tier: driver.RestoreTierExpedited})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("Expedited on DEEP_ARCHIVE err = %v, want InvalidArgument", err)
	}

	_, err = m.RestoreObject(ctx, "b", "glacier", "", driver.RestoreRequest{Days: 1, Tier: driver.RestoreTierExpedited})
	requireNoError(t, err)

	_, err = m.RestoreObject(ctx, "b", "missing", "", driver.RestoreRequest{Days: 1})
	if !cerrors.IsNotFound(err) {
		t.Fatalf("missing key err = %v, want NotFound", err)
	}
}

func TestRestoreVersionAndCopy(t *testing.T) {
	ctx := context.Background()
	m, _ := newRestoreMock(t, false)

	requireNoError(t, m.SetVersioningStatus(ctx, "b", versioningEnabled))
	putClass(t, m, "k", storageClassGlacier)

	v1, err := m.HeadObject(ctx, "b", "k")
	requireNoError(t, err)

	// A newer STANDARD version becomes current; v1 stays archived.
	putClass(t, m, "k", "")

	_, err = m.GetObjectVersion(ctx, "b", "k", v1.VersionID)
	assertInvalidObjectState(t, err, storageClassGlacier)

	_, err = m.CopyObjectV2(ctx, &driver.CopyObjectRequest{
		DstBucket: "b", DstKey: "copy", Src: driver.CopySource{Bucket: "b", Key: "k"}, SrcVersionID: v1.VersionID,
	})
	assertInvalidObjectState(t, err, storageClassGlacier)

	_, err = m.RestoreObject(ctx, "b", "k", v1.VersionID, driver.RestoreRequest{Days: 2})
	requireNoError(t, err)

	obj, err := m.GetObjectVersion(ctx, "b", "k", v1.VersionID)
	requireNoError(t, err)
	assertEqual(t, storageClassGlacier, obj.Info.StorageClass)

	if obj.Info.Restore == nil {
		t.Fatal("restored version reports no Restore")
	}

	_, err = m.CopyObjectV2(ctx, &driver.CopyObjectRequest{
		DstBucket: "b", DstKey: "copy", Src: driver.CopySource{Bucket: "b", Key: "k"}, SrcVersionID: v1.VersionID,
	})
	requireNoError(t, err)

	// The current STANDARD version is not affected by the version restore.
	cur, err := m.HeadObject(ctx, "b", "k")
	requireNoError(t, err)

	if cur.Restore != nil {
		t.Fatalf("current version Restore = %+v, want nil", cur.Restore)
	}
}

// TestRestoreSurvivesLockChange verifies a retention change on the current
// version (which rebuilds the current object from the version chain) keeps
// the storage class and restore state.
func TestRestoreSurvivesLockChange(t *testing.T) {
	ctx := context.Background()
	m, _ := newRestoreMock(t, false)

	requireNoError(t, m.EnableObjectLock(ctx, "b"))
	putClass(t, m, "k", storageClassGlacier)

	_, err := m.RestoreObject(ctx, "b", "k", "", driver.RestoreRequest{Days: 2})
	requireNoError(t, err)

	requireNoError(t, m.PutObjectLegalHold(ctx, "b", "k", "", true))

	obj, err := m.GetObject(ctx, "b", "k")
	requireNoError(t, err)
	assertEqual(t, storageClassGlacier, obj.Info.StorageClass)

	if obj.Info.Restore == nil {
		t.Fatal("Restore lost after a legal-hold change")
	}
}

func TestRestoreExpiryRounding(t *testing.T) {
	tests := []struct {
		done time.Time
		days int
		want time.Time
	}{
		{time.Date(2012, 10, 15, 10, 30, 0, 0, time.UTC), 3, time.Date(2012, 10, 19, 0, 0, 0, 0, time.UTC)},
		{time.Date(2012, 10, 15, 0, 0, 0, 0, time.UTC), 1, time.Date(2012, 10, 17, 0, 0, 0, 0, time.UTC)},
		{time.Date(2012, 12, 31, 23, 59, 0, 0, time.UTC), 1, time.Date(2013, 1, 2, 0, 0, 0, 0, time.UTC)},
	}

	for _, tc := range tests {
		if got := restoreExpiry(tc.done, tc.days); !got.Equal(tc.want) {
			t.Fatalf("restoreExpiry(%v, %d) = %v, want %v", tc.done, tc.days, got, tc.want)
		}
	}
}
