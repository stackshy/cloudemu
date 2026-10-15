package s3

import (
	"context"
	"errors"
	"testing"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// assertRetention checks key's current retention is mode until want.
func assertRetention(t *testing.T, m *Mock, key, mode string, want time.Time) {
	t.Helper()

	ret, err := m.GetObjectRetention(context.Background(), "b", key, "")
	requireNoError(t, err)

	if ret.Mode != mode || !ret.RetainUntilDate.Equal(want) {
		t.Fatalf("%s retention = %s until %v, want %s until %v", key, ret.Mode, ret.RetainUntilDate, mode, want)
	}
}

func TestPutObjectLockConfigurationRequiresVersioning(t *testing.T) {
	m, _ := newLockMock()
	ctx := context.Background()

	requireNoError(t, m.CreateBucket(ctx, "b"))

	_, err := m.GetObjectLockConfiguration(ctx, "b")
	if !errors.Is(err, driver.ErrNoObjectLockConfiguration) {
		t.Fatalf("Get on a plain bucket err = %v, want ErrNoObjectLockConfiguration", err)
	}

	err = m.PutObjectLockConfiguration(ctx, "b", driver.ObjectLockConfiguration{})
	if !errors.Is(err, driver.ErrObjectLockNeedsVersioning) {
		t.Fatalf("Put on an unversioned bucket err = %v, want ErrObjectLockNeedsVersioning", err)
	}

	requireNoError(t, m.SetVersioningStatus(ctx, "b", versioningSuspended))

	err = m.PutObjectLockConfiguration(ctx, "b", driver.ObjectLockConfiguration{})
	if !errors.Is(err, driver.ErrObjectLockNeedsVersioning) {
		t.Fatalf("Put on a suspended bucket err = %v, want ErrObjectLockNeedsVersioning", err)
	}
}

// TestPutObjectLockConfigurationEnablesLock is the issue's core case: enabling
// Object Lock on an existing versioned bucket makes retention and legal hold
// work, and the bucket can no longer suspend versioning.
func TestPutObjectLockConfigurationEnablesLock(t *testing.T) {
	m, fc := newLockMock()
	ctx := context.Background()

	requireNoError(t, m.CreateBucket(ctx, "b"))
	requireNoError(t, m.SetVersioningStatus(ctx, "b", versioningEnabled))
	requireNoError(t, m.PutObject(ctx, "b", "k", []byte("v1"), "", nil))
	requireNoError(t, m.PutObjectLockConfiguration(ctx, "b", driver.ObjectLockConfiguration{}))

	cfg, err := m.GetObjectLockConfiguration(ctx, "b")
	requireNoError(t, err)
	assertEqual(t, "", cfg.DefaultMode)

	until := fc.Now().Add(time.Hour)
	requireNoError(t, m.PutObjectRetention(ctx, "b", "k", "",
		driver.ObjectRetention{Mode: driver.ObjectLockGovernance, RetainUntilDate: until}, false))
	requireNoError(t, m.PutObjectLegalHold(ctx, "b", "k", "", true))

	if err := m.SetVersioningStatus(ctx, "b", versioningSuspended); err == nil {
		t.Fatal("versioning suspended on an Object Lock bucket")
	}
}

func TestDefaultRetentionAppliesToNewVersions(t *testing.T) {
	m, fc := newLockMock()
	ctx := context.Background()
	now := fc.Now()

	requireNoError(t, m.CreateBucket(ctx, "b"))
	requireNoError(t, m.SetVersioningStatus(ctx, "b", versioningEnabled))
	requireNoError(t, m.PutObject(ctx, "b", "before", []byte("x"), "", nil))
	requireNoError(t, m.PutObjectLockConfiguration(ctx, "b",
		driver.ObjectLockConfiguration{DefaultMode: driver.ObjectLockCompliance, DefaultDays: 2}))

	// Existing versions are not touched.
	ret, err := m.GetObjectRetention(ctx, "b", "before", "")
	requireNoError(t, err)
	assertEqual(t, "", ret.Mode)

	requireNoError(t, m.PutObject(ctx, "b", "put", []byte("x"), "", nil))
	assertRetention(t, m, "put", driver.ObjectLockCompliance, now.AddDate(0, 0, 2))

	_, err = m.CopyObjectV2(ctx, &driver.CopyObjectRequest{DstBucket: "b", DstKey: "copy", Src: driver.CopySource{Bucket: "b", Key: "before"}})
	requireNoError(t, err)
	assertRetention(t, m, "copy", driver.ObjectLockCompliance, now.AddDate(0, 0, 2))

	mp, err := m.CreateMultipartUpload(ctx, "b", "mp", "text/plain")
	requireNoError(t, err)
	part, err := m.UploadPart(ctx, "b", "mp", mp.UploadID, 1, []byte("x"))
	requireNoError(t, err)
	requireNoError(t, m.CompleteMultipartUpload(ctx, "b", "mp", mp.UploadID, []driver.UploadPart{{PartNumber: 1, ETag: part.ETag}}))
	assertRetention(t, m, "mp", driver.ObjectLockCompliance, now.AddDate(0, 0, 2))

	// The default protects the version: a permanent delete is refused.
	_, _, err = m.DeleteObjectVersionWithBypass(ctx, "b", "put", currentVersionID(t, m, "b", "put"), true)
	assertPermissionDenied(t, err)

	// An explicit, shorter GOVERNANCE retention on the write replaces the
	// default for that version.
	requireNoError(t, m.PutObjectWithLock(ctx, "b", "explicit", []byte("x"), "", nil, nil,
		driver.ObjectLockSettings{Mode: driver.ObjectLockGovernance, RetainUntil: now.Add(time.Hour), LegalHold: true}))
	assertRetention(t, m, "explicit", driver.ObjectLockGovernance, now.Add(time.Hour))

	on, err := m.GetObjectLegalHold(ctx, "b", "explicit", "")
	requireNoError(t, err)
	assertEqual(t, true, on)

	// A legal hold alone keeps the default retention.
	requireNoError(t, m.PutObjectWithLock(ctx, "b", "hold", []byte("x"), "", nil, nil, driver.ObjectLockSettings{LegalHold: true}))
	assertRetention(t, m, "hold", driver.ObjectLockCompliance, now.AddDate(0, 0, 2))

	// Years, then clearing the default.
	requireNoError(t, m.PutObjectLockConfiguration(ctx, "b",
		driver.ObjectLockConfiguration{DefaultMode: driver.ObjectLockGovernance, DefaultYears: 1}))
	requireNoError(t, m.PutObject(ctx, "b", "year", []byte("x"), "", nil))
	assertRetention(t, m, "year", driver.ObjectLockGovernance, now.AddDate(1, 0, 0))

	requireNoError(t, m.PutObjectLockConfiguration(ctx, "b", driver.ObjectLockConfiguration{}))
	requireNoError(t, m.PutObject(ctx, "b", "none", []byte("x"), "", nil))

	ret, err = m.GetObjectRetention(ctx, "b", "none", "")
	requireNoError(t, err)
	assertEqual(t, "", ret.Mode)
}

func TestObjectLockConfigurationValidation(t *testing.T) {
	m, _ := newLockMock()
	ctx := context.Background()

	requireNoError(t, m.CreateBucket(ctx, "b"))
	requireNoError(t, m.SetVersioningStatus(ctx, "b", versioningEnabled))

	for _, cfg := range []driver.ObjectLockConfiguration{
		{DefaultMode: driver.ObjectLockGovernance},
		{DefaultMode: driver.ObjectLockGovernance, DefaultDays: 1, DefaultYears: 1},
		{DefaultMode: driver.ObjectLockGovernance, DefaultDays: -1},
		{DefaultMode: "FOREVER", DefaultDays: 1},
	} {
		if err := m.PutObjectLockConfiguration(ctx, "b", cfg); !cerrors.IsInvalidArgument(err) {
			t.Fatalf("PutObjectLockConfiguration(%+v) err = %v, want InvalidArgument", cfg, err)
		}
	}

	// A rejected configuration does not enable Object Lock.
	if _, err := m.GetObjectLockConfiguration(ctx, "b"); !errors.Is(err, driver.ErrNoObjectLockConfiguration) {
		t.Fatalf("Get after rejected Put err = %v, want ErrNoObjectLockConfiguration", err)
	}

	err := m.PutObjectWithLock(ctx, "b", "k", []byte("x"), "", nil, nil, driver.ObjectLockSettings{LegalHold: true})
	if !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("PutObjectWithLock on a bucket without lock err = %v, want FailedPrecondition", err)
	}

	if _, err := m.HeadObject(ctx, "b", "k"); !cerrors.IsNotFound(err) {
		t.Fatalf("rejected PutObjectWithLock stored the object: %v", err)
	}
}

func TestObjectLockConfigurationCreatedWithHeaderAndSnapshot(t *testing.T) {
	m, _ := newLockMock()
	ctx := context.Background()

	requireNoError(t, m.CreateBucket(ctx, "b"))
	requireNoError(t, m.EnableObjectLock(ctx, "b"))

	cfg, err := m.GetObjectLockConfiguration(ctx, "b")
	requireNoError(t, err)
	assertEqual(t, "", cfg.DefaultMode)

	requireNoError(t, m.PutObjectLockConfiguration(ctx, "b",
		driver.ObjectLockConfiguration{DefaultMode: driver.ObjectLockGovernance, DefaultDays: 5}))

	snap, err := m.Snapshot(ctx, true)
	requireNoError(t, err)

	restored, _ := newLockMock()
	requireNoError(t, restored.Restore(ctx, snap))

	cfg, err = restored.GetObjectLockConfiguration(ctx, "b")
	requireNoError(t, err)

	if cfg.DefaultMode != driver.ObjectLockGovernance || cfg.DefaultDays != 5 {
		t.Fatalf("restored config = %+v, want GOVERNANCE 5 days", cfg)
	}
}
