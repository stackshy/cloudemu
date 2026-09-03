package gcs

import (
	"context"
	"net/http"
	"strconv"

	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// retentionStore is the optional capability that persists a bucket's retention
// policy (WORM) — set/lock/read — so Buckets.patch of retentionPolicy and the
// Buckets.lockRetentionPolicy endpoint round-trip and are enforced on
// delete/overwrite. Nil makes retentionPolicy absent and unsettable.
type retentionStore interface {
	SetBucketRetentionPolicyGCS(ctx context.Context, bucket string, periodSeconds int64) error
	LockBucketRetentionPolicyGCS(ctx context.Context, bucket string) error
	BucketRetentionPolicyGCS(ctx context.Context, bucket string) (*storagedriver.GCSRetentionPolicy, error)
}

// applyRetention persists an incoming retentionPolicy block (from create or
// patch). A nil block leaves the policy untouched (GCS merge-patch semantics; a
// JSON null also decodes to nil, so removal-by-null is not distinguishable and
// is a known limitation). A shorten/remove of a locked policy returns the
// backing *GCSImmutableError.
func (h *Handler) applyRetention(ctx context.Context, bucket string, rp *retentionPolicy) error {
	if h.retention == nil || rp == nil {
		return nil
	}

	period, err := strconv.ParseInt(rp.RetentionPeriod, 10, 64)
	if err != nil {
		// A malformed/empty retentionPeriod is treated as removal (period 0).
		period = 0
	}

	return h.retention.SetBucketRetentionPolicyGCS(ctx, bucket, period)
}

// retentionView renders a bucket's stored retention policy, or nil when the
// bucket has no policy (or no backing store).
func (h *Handler) retentionView(ctx context.Context, bucket string) *retentionPolicy {
	if h.retention == nil {
		return nil
	}

	pol, err := h.retention.BucketRetentionPolicyGCS(ctx, bucket)
	if err != nil || pol == nil {
		return nil
	}

	return &retentionPolicy{
		RetentionPeriod: strconv.FormatInt(pol.RetentionPeriod, 10),
		EffectiveTime:   pol.EffectiveTime,
		IsLocked:        pol.IsLocked,
	}
}

// lockRetentionPolicy handles POST /b/{bucket}/lockRetentionPolicy, making the
// bucket's retention policy permanent (it can then only be increased).
func (h *Handler) lockRetentionPolicy(w http.ResponseWriter, r *http.Request, bucket string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
		return
	}

	if h.retention == nil {
		writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "retention policy not supported")
		return
	}

	if err := h.retention.LockBucketRetentionPolicyGCS(r.Context(), bucket); err != nil {
		writePreconditionOrErr(w, err)
		return
	}

	if h.ext != nil {
		_ = h.ext.TouchBucket(r.Context(), bucket)
	}

	writeJSON(w, http.StatusOK, h.bucketView(r, bucket, ""))
}
