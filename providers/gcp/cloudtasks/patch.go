package cloudtasks

import (
	"strings"

	"github.com/stackshy/cloudemu/v2/services/cloudtasks/driver"
)

// applyMask replaces the fields of q named in mask with those from cfg. An empty
// mask replaces every mutable field (real Cloud Tasks' "replace the whole
// resource" behavior when updateMask is omitted). A dotted sub-field mask entry
// (e.g. "rateLimits.maxDispatchesPerSecond") is honored at its top-level
// segment.
func applyMask(q *driver.Queue, cfg driver.QueueConfig, mask []string) {
	if maskHas(mask, "appEngineRoutingOverride") {
		q.AppEngineRoutingOverride = cloneAppEngineRouting(cfg.AppEngineRoutingOverride)
	}

	if maskHas(mask, "rateLimits") {
		q.RateLimits = cloneRateLimits(cfg.RateLimits)
	}

	if maskHas(mask, "retryConfig") {
		q.RetryConfig = cloneRetryConfig(cfg.RetryConfig)
	}

	if maskHas(mask, "stackdriverLoggingConfig") {
		q.StackdriverLoggingConfig = cloneLoggingConfig(cfg.StackdriverLoggingConfig)
	}

	if maskHas(mask, "httpTarget") {
		q.HTTPTarget = cloneBytes(cfg.HTTPTarget)
	}
}

// maskHas reports whether mask names field. An empty mask means "replace
// everything". A dotted entry matches on its leading segment.
func maskHas(mask []string, field string) bool {
	if len(mask) == 0 {
		return true
	}

	for _, m := range mask {
		m = strings.TrimSpace(m)
		if head, _, ok := strings.Cut(m, "."); ok {
			m = head
		}

		if m == field {
			return true
		}
	}

	return false
}
