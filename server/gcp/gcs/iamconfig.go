package gcs

import "context"

// defaultPublicAccessPrevention is what GCS reports for a bucket that never set
// the field explicitly.
const defaultPublicAccessPrevention = "inherited"

// iamConfigStore is the optional capability that persists a bucket's
// iamConfiguration — Uniform Bucket-Level Access (with its lockedTime) and
// Public Access Prevention — so a Buckets.patch of these round-trips on GET
// instead of reading back a hardcoded default.
type iamConfigStore interface {
	SetBucketIAMConfigGCS(ctx context.Context, bucket string, ublaEnabled *bool, publicAccessPrevention string) error
	BucketIAMConfigGCS(ctx context.Context, bucket string) (enabled bool, lockedTime, publicAccessPrevention string, err error)
}

// applyIAMConfig persists an incoming iamConfiguration block (from create or
// patch). A nil block leaves the bucket's config untouched.
func (h *Handler) applyIAMConfig(ctx context.Context, bucket string, cfg *iamConfiguration) error {
	if h.iamCfg == nil || cfg == nil {
		return nil
	}

	var ubla *bool

	if cfg.UniformBucketLevelAccess != nil {
		enabled := cfg.UniformBucketLevelAccess.Enabled
		ubla = &enabled
	}

	if ubla == nil && cfg.PublicAccessPrevention == "" {
		return nil
	}

	return h.iamCfg.SetBucketIAMConfigGCS(ctx, bucket, ubla, cfg.PublicAccessPrevention)
}

// iamConfigView renders a bucket's stored iamConfiguration, falling back to the
// GCS defaults (UBLA disabled, publicAccessPrevention "inherited") when the
// backing driver doesn't persist it.
func (h *Handler) iamConfigView(ctx context.Context, bucket string) *iamConfiguration {
	cfg := &iamConfiguration{PublicAccessPrevention: defaultPublicAccessPrevention}

	if h.iamCfg == nil {
		return cfg
	}

	enabled, lockedTime, pap, err := h.iamCfg.BucketIAMConfigGCS(ctx, bucket)
	if err != nil {
		return cfg
	}

	if pap != "" {
		cfg.PublicAccessPrevention = pap
	}

	if enabled {
		cfg.UniformBucketLevelAccess = &uniformBucketLevelAccess{Enabled: true, LockedTime: lockedTime}
	}

	return cfg
}
