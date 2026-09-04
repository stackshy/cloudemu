package gcs

import (
	"context"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// ublaLockDays is how far ahead GCS stamps a bucket's uniform bucket-level
// access lockedTime when UBLA is enabled — the window during which it can still
// be disabled before becoming permanent.
const ublaLockDays = 90

// iamConfigStore mirrors the server-side capability the GCS wire handler
// type-asserts for; declaring it here compile-checks the Mock keeps the exact
// signatures the handler needs.
var _ interface {
	SetBucketIAMConfigGCS(ctx context.Context, bucket string, ublaEnabled *bool, publicAccessPrevention string) error
	BucketIAMConfigGCS(ctx context.Context, bucket string) (enabled bool, lockedTime, publicAccessPrevention string, err error)
} = (*Mock)(nil)

// SetBucketIAMConfigGCS updates the bucket's iamConfiguration. A non-nil
// ublaEnabled toggles Uniform Bucket-Level Access, stamping lockedTime on
// enable and clearing it on disable; a non-empty publicAccessPrevention sets
// that field. Both are left unchanged when their argument is nil/empty, matching
// the JSON merge-patch the Buckets.patch endpoint applies.
func (m *Mock) SetBucketIAMConfigGCS(_ context.Context, bucket string, ublaEnabled *bool, publicAccessPrevention string) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.mu.Lock()
	defer bkt.mu.Unlock()

	if ublaEnabled != nil {
		if *ublaEnabled {
			bkt.ublaEnabled = true
			// Preserve an existing lock window across a redundant enable; stamp a
			// fresh one only on the enable transition.
			if bkt.ublaLockedTime == "" {
				lock := m.opts.Clock.Now().UTC().Add(ublaLockDays * gcsHoursPerDay * time.Hour)
				bkt.ublaLockedTime = lock.Format(gcsTimeFormat)
			}
		} else {
			bkt.ublaEnabled = false
			bkt.ublaLockedTime = ""
		}
	}

	if publicAccessPrevention != "" {
		bkt.publicAccessPrevention = publicAccessPrevention
	}

	return nil
}

// BucketIAMConfigGCS returns the bucket's UBLA enablement, its lockedTime (empty
// when UBLA is off), and its public-access-prevention setting.
func (m *Mock) BucketIAMConfigGCS(_ context.Context, bucket string) (enabled bool, lockedTime, publicAccessPrevention string, err error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return false, "", "", cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.mu.Lock()
	defer bkt.mu.Unlock()

	return bkt.ublaEnabled, bkt.ublaLockedTime, bkt.publicAccessPrevention, nil
}
