package gcs

import (
	"context"

	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// putCORS applies a bucket's cors[] configuration. An empty (but present) rule
// set clears the configuration, mirroring real GCS where a Buckets patch with
// "cors":[] removes all CORS rules.
func (h *Handler) putCORS(ctx context.Context, bucket string, rules []corsRule) error {
	if len(rules) == 0 {
		return h.bucket.DeleteCORSConfig(ctx, bucket)
	}

	cfg := storagedriver.CORSConfig{Rules: make([]storagedriver.CORSRule, 0, len(rules))}
	for _, rule := range rules {
		cfg.Rules = append(cfg.Rules, storagedriver.CORSRule{
			AllowedOrigins: rule.Origin,
			AllowedMethods: rule.Method,
			ExposeHeaders:  rule.ResponseHeader,
			MaxAgeSeconds:  rule.MaxAgeSeconds,
		})
	}

	return h.bucket.PutCORSConfig(ctx, bucket, cfg)
}

// corsView renders the stored CORS configuration for a bucket as the GCS cors[]
// array, or nil when none is set.
func (h *Handler) corsView(ctx context.Context, bucket string) []corsRule {
	cfg, err := h.bucket.GetCORSConfig(ctx, bucket)
	if err != nil || cfg == nil || len(cfg.Rules) == 0 {
		return nil
	}

	out := make([]corsRule, 0, len(cfg.Rules))
	for _, rule := range cfg.Rules {
		out = append(out, corsRule{
			Origin:         rule.AllowedOrigins,
			Method:         rule.AllowedMethods,
			ResponseHeader: rule.ExposeHeaders,
			MaxAgeSeconds:  rule.MaxAgeSeconds,
		})
	}

	return out
}
