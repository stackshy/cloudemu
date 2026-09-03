package gcs

import (
	"context"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// retentionStore mirrors the server-side capability the GCS wire handler
// type-asserts for the bucket retention policy (WORM). Declaring it here
// compile-checks the Mock keeps the exact signatures the handler needs.
var _ interface {
	SetBucketRetentionPolicyGCS(ctx context.Context, bucket string, periodSeconds int64) error
	LockBucketRetentionPolicyGCS(ctx context.Context, bucket string) error
	BucketRetentionPolicyGCS(ctx context.Context, bucket string) (*driver.GCSRetentionPolicy, error)
} = (*Mock)(nil)

// SetBucketRetentionPolicyGCS sets or updates the bucket retention period (in
// seconds). A locked policy can only be increased — attempting to shorten or
// remove it returns a *driver.GCSImmutableError. A period of 0 on an unlocked
// bucket removes the policy. effectiveTime is stamped once, when the policy is
// first established, and preserved across later increases.
func (m *Mock) SetBucketRetentionPolicyGCS(_ context.Context, bucket string, periodSeconds int64) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.mu.Lock()
	defer bkt.mu.Unlock()

	if bkt.retentionLocked && periodSeconds < bkt.retentionPeriod {
		return &driver.GCSImmutableError{
			Reason:  "retentionPolicyNotMet",
			Message: "cannot shorten or remove a locked retention policy",
		}
	}

	if periodSeconds <= 0 {
		bkt.retentionPeriod = 0
		bkt.retentionEffectiveTime = ""

		return nil
	}

	if bkt.retentionEffectiveTime == "" {
		bkt.retentionEffectiveTime = m.opts.Clock.Now().UTC().Format(gcsTimeFormat)
	}

	bkt.retentionPeriod = periodSeconds

	return nil
}

// LockBucketRetentionPolicyGCS makes the bucket's current retention policy
// permanent. Locking a bucket without a retention policy is rejected.
func (m *Mock) LockBucketRetentionPolicyGCS(_ context.Context, bucket string) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.mu.Lock()
	defer bkt.mu.Unlock()

	if bkt.retentionPeriod <= 0 {
		return cerrors.Newf(cerrors.FailedPrecondition, "bucket %q has no retention policy to lock", bucket)
	}

	bkt.retentionLocked = true

	return nil
}

// BucketRetentionPolicyGCS returns the bucket's retention policy, or nil when no
// policy is set.
func (m *Mock) BucketRetentionPolicyGCS(_ context.Context, bucket string) (*driver.GCSRetentionPolicy, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.mu.Lock()
	defer bkt.mu.Unlock()

	if bkt.retentionPeriod <= 0 {
		return nil, nil //nolint:nilnil // no policy is a valid, non-error state
	}

	return &driver.GCSRetentionPolicy{
		RetentionPeriod: bkt.retentionPeriod,
		EffectiveTime:   bkt.retentionEffectiveTime,
		IsLocked:        bkt.retentionLocked,
	}, nil
}

// objectImmutable takes bkt.mu and delegates to immutableLocked. Callers that
// do not already hold bkt.mu use this variant. It returns a
// *driver.GCSImmutableError when the object is protected, else nil.
func objectImmutable(m *Mock, bkt *bucketMeta, obj *gcsObject) error {
	bkt.mu.Lock()
	defer bkt.mu.Unlock()

	return m.immutableLocked(bkt, obj)
}

// immutableLocked reports whether obj is protected by an active hold or an
// unelapsed retention period. The caller MUST hold bkt.mu. Object hold flags are
// immutable per generation (written copy-on-write), so reading them without the
// lock is safe; the retention period/effectiveTime are bkt.mu-guarded.
func (m *Mock) immutableLocked(bkt *bucketMeta, obj *gcsObject) error {
	if obj.TemporaryHold {
		return &driver.GCSImmutableError{
			Reason:  "objectUnderActiveHold",
			Message: "object is under an active temporary hold and cannot be deleted or overwritten",
		}
	}

	if obj.EventBasedHold {
		return &driver.GCSImmutableError{
			Reason:  "objectUnderActiveHold",
			Message: "object is under an active event-based hold and cannot be deleted or overwritten",
		}
	}

	if bkt.retentionPeriod <= 0 {
		return nil
	}

	ref := retentionRefTime(obj)
	expiry := ref.Add(time.Duration(bkt.retentionPeriod) * time.Second)

	if m.opts.Clock.Now().UTC().Before(expiry) {
		return &driver.GCSImmutableError{
			Reason: "retentionPolicyNotMet",
			Message: "object is subject to the bucket's retention policy and cannot be deleted or " +
				"overwritten until " + expiry.UTC().Format(gcsTimeFormat),
		}
	}

	return nil
}

// retentionExpiration returns the object's retentionExpirationTime under the
// bucket's current retention policy, or "" when the bucket has no policy or an
// event-based hold is currently pinning the object (its clock is paused). It
// acquires bkt.mu to read the retention period.
func retentionExpiration(bkt *bucketMeta, obj *gcsObject) string {
	bkt.mu.Lock()
	defer bkt.mu.Unlock()

	if bkt.retentionPeriod <= 0 || obj.EventBasedHold {
		return ""
	}

	ref := retentionRefTime(obj)

	return ref.Add(time.Duration(bkt.retentionPeriod) * time.Second).UTC().Format(gcsTimeFormat)
}

// retentionRefTime resolves the instant from which retention is measured for an
// object: its RetentionRef (reset when an event-based hold is released), falling
// back to Created, then to the zero time when neither parses.
func retentionRefTime(obj *gcsObject) time.Time {
	for _, s := range []string{obj.RetentionRef, obj.Created} {
		if s == "" {
			continue
		}

		if t, err := time.Parse(gcsTimeFormat, s); err == nil {
			return t
		}
	}

	return time.Time{}
}
