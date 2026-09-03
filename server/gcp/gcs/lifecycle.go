package gcs

import (
	"context"
	"encoding/json"
	"strconv"

	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// lifecycleRawStore is the optional capability that persists a bucket's
// lifecycle configuration verbatim as GCS JSON, preserving every rule condition
// (numNewerVersions, isLive, createdBefore, matchesStorageClass, …) rather than
// the age-only subset the portable driver.LifecycleConfig can express.
type lifecycleRawStore interface {
	SetLifecycleGCS(ctx context.Context, bucket string, doc []byte) error
	GetLifecycleGCS(ctx context.Context, bucket string) ([]byte, bool, error)
}

// putLifecycle stores lc for the named bucket. When the backing driver supports
// verbatim lifecycle storage every condition is preserved; otherwise it falls
// back to the age-only portable config so nothing regresses.
func (h *Handler) putLifecycle(ctx context.Context, bucket string, lc *bucketLifecycle) error {
	if h.lifecycle != nil {
		doc, err := json.Marshal(lc)
		if err != nil {
			return err
		}

		return h.lifecycle.SetLifecycleGCS(ctx, bucket, doc)
	}

	return h.bucket.PutLifecycleConfig(ctx, bucket, toLifecycleConfig(lc))
}

// lifecycleView renders the stored lifecycle configuration for a bucket, or nil
// when none is set. It prefers the verbatim capability, falling back to the
// portable config so a lifecycle set through the typed API still surfaces.
func (h *Handler) lifecycleView(ctx context.Context, bucket string) *bucketLifecycle {
	if h.lifecycle != nil {
		if doc, ok, err := h.lifecycle.GetLifecycleGCS(ctx, bucket); err == nil && ok && len(doc) > 0 {
			var lc bucketLifecycle
			if json.Unmarshal(doc, &lc) == nil && len(lc.Rule) > 0 {
				return &lc
			}
		}
	}

	if cfg, err := h.bucket.GetLifecycleConfig(ctx, bucket); err == nil && cfg != nil && len(cfg.Rules) > 0 {
		return fromLifecycleConfig(cfg)
	}

	return nil
}

// toLifecycleConfig converts the GCS lifecycle JSON into the driver's rule set,
// the fallback path when the driver lacks verbatim lifecycle storage. Only the
// age-based Delete/SetStorageClass subset survives this projection.
func toLifecycleConfig(lc *bucketLifecycle) storagedriver.LifecycleConfig {
	cfg := storagedriver.LifecycleConfig{Rules: make([]storagedriver.LifecycleRule, 0, len(lc.Rule))}

	for i := range lc.Rule {
		r := &lc.Rule[i]
		rule := storagedriver.LifecycleRule{ID: strconv.Itoa(i), Enabled: true}

		age := 0
		if r.Condition.Age != nil {
			age = *r.Condition.Age
		}

		switch r.Action.Type {
		case "Delete":
			rule.ExpirationDays = age
		case "SetStorageClass":
			rule.TransitionDays = age
			rule.TransitionStorageClass = r.Action.StorageClass
		}

		cfg.Rules = append(cfg.Rules, rule)
	}

	return cfg
}

// fromLifecycleConfig renders the driver's lifecycle rules back into GCS JSON
// (the fallback read path).
func fromLifecycleConfig(cfg *storagedriver.LifecycleConfig) *bucketLifecycle {
	lc := &bucketLifecycle{Rule: make([]lifecycleRule, 0, len(cfg.Rules))}

	for i := range cfg.Rules {
		rule := &cfg.Rules[i]

		switch {
		case rule.TransitionStorageClass != "":
			age := rule.TransitionDays
			lc.Rule = append(lc.Rule, lifecycleRule{
				Action:    lifecycleAction{Type: "SetStorageClass", StorageClass: rule.TransitionStorageClass},
				Condition: lifecycleCondition{Age: &age},
			})
		case rule.ExpirationDays > 0:
			age := rule.ExpirationDays
			lc.Rule = append(lc.Rule, lifecycleRule{
				Action:    lifecycleAction{Type: "Delete"},
				Condition: lifecycleCondition{Age: &age},
			})
		}
	}

	return lc
}
