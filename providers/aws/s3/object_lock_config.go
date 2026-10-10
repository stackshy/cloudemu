package s3

import (
	"context"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// objectLockDefault is a bucket's default Object Lock retention: every new
// object version written without an explicit retention gets mode, retained
// for days or years from the write.
type objectLockDefault struct {
	mode  string
	days  int
	years int
}

// retainUntil is the retain-until instant of a version written at now.
func (d *objectLockDefault) retainUntil(now time.Time) time.Time {
	return now.AddDate(d.years, 0, d.days)
}

// applyDefaultRetentionLocked gives obj the bucket's default retention when it
// carries no explicit retention. Callers hold versionsMu.
func (m *Mock) applyDefaultRetentionLocked(bkt *bucketMeta, obj *s3Object) {
	if bkt.lockDefault == nil || obj.lock.retentionMode != "" {
		return
	}

	obj.lock.retentionMode = bkt.lockDefault.mode
	obj.lock.retainUntil = bkt.lockDefault.retainUntil(m.opts.Clock.Now())
}

// validLockMode reports whether mode is an S3 Object Lock retention mode.
func validLockMode(mode string) bool {
	return mode == driver.ObjectLockGovernance || mode == driver.ObjectLockCompliance
}

// PutObjectLockConfiguration implements driver.ObjectLockBucket: it enables
// Object Lock on the bucket and sets (or, with an empty DefaultMode, clears)
// its default retention. Enabling requires versioning to be Enabled; once on,
// Object Lock cannot be turned off.
func (m *Mock) PutObjectLockConfiguration(_ context.Context, bucket string, cfg driver.ObjectLockConfiguration) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	var def *objectLockDefault

	if cfg.DefaultMode != "" {
		if !validLockMode(cfg.DefaultMode) || (cfg.DefaultDays > 0) == (cfg.DefaultYears > 0) ||
			cfg.DefaultDays < 0 || cfg.DefaultYears < 0 {
			return cerrors.New(cerrors.InvalidArgument, "Default retention period must be a positive integer value")
		}

		def = &objectLockDefault{mode: cfg.DefaultMode, days: cfg.DefaultDays, years: cfg.DefaultYears}
	}

	bkt.versionsMu.Lock()
	defer bkt.versionsMu.Unlock()

	if bkt.versionStatus != versioningEnabled {
		return driver.ErrObjectLockNeedsVersioning
	}

	bkt.objectLockEnabled = true
	bkt.lockDefault = def

	return nil
}

// GetObjectLockConfiguration implements driver.ObjectLockBucket.
func (m *Mock) GetObjectLockConfiguration(_ context.Context, bucket string) (*driver.ObjectLockConfiguration, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.versionsMu.Lock()
	defer bkt.versionsMu.Unlock()

	if !bkt.objectLockEnabled {
		return nil, driver.ErrNoObjectLockConfiguration
	}

	cfg := &driver.ObjectLockConfiguration{}
	if d := bkt.lockDefault; d != nil {
		cfg.DefaultMode, cfg.DefaultDays, cfg.DefaultYears = d.mode, d.days, d.years
	}

	return cfg, nil
}

// PutObjectWithLock implements driver.ObjectLockBucket: PutObjectWithSystemProps
// with the new version's Object Lock settings applied in the same write. The
// bucket must have Object Lock enabled.
func (m *Mock) PutObjectWithLock(
	ctx context.Context, bucket, key string, data []byte, contentType string,
	metadata map[string]string, props *driver.ObjectSystemProps, lock driver.ObjectLockSettings,
) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	if lock.Mode != "" && !validLockMode(lock.Mode) {
		return cerrors.New(cerrors.InvalidArgument, "Unknown wormMode directive.")
	}

	bkt.versionsMu.Lock()
	enabled := bkt.objectLockEnabled
	bkt.versionsMu.Unlock()

	if !enabled {
		return objectLockMissingError()
	}

	return m.putObject(ctx, bkt, bucket, key, data, contentType, metadata, props,
		objectLock{retentionMode: lock.Mode, retainUntil: lock.RetainUntil, legalHold: lock.LegalHold})
}
