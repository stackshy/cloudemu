package scheduler

import (
	"strings"

	"github.com/stackshy/cloudemu/v2/services/scheduler/driver"
)

// applyMask replaces the fields of job named in mask with those from cfg. An
// empty mask replaces every field (real Cloud Scheduler's "replace the whole
// resource" behavior when updateMask is omitted). A dotted sub-field mask entry
// (e.g. "retryConfig.retryCount") is honored at its top-level segment.
//
//nolint:gocritic // hugeParam: cfg mirrors the driver interface's value semantics.
func applyMask(job *driver.Job, cfg driver.JobConfig, mask []string) {
	applyScalarMask(job, cfg, mask)
	applyTargetMask(job, cfg, mask)
}

// applyScalarMask patches the scalar and retry fields.
//
//nolint:gocritic // hugeParam: cfg mirrors the driver interface's value semantics.
func applyScalarMask(job *driver.Job, cfg driver.JobConfig, mask []string) {
	if maskHas(mask, "description") {
		job.Description = cfg.Description
	}

	if maskHas(mask, "schedule") {
		job.Schedule = cfg.Schedule
	}

	if maskHas(mask, "timeZone") {
		job.TimeZone = cfg.TimeZone
	}

	if maskHas(mask, "attemptDeadline") {
		job.AttemptDeadline = cfg.AttemptDeadline
	}

	if maskHas(mask, "retryConfig") {
		job.RetryConfig = cloneRetryConfig(cfg.RetryConfig)
	}
}

// applyTargetMask patches the target oneof fields.
//
//nolint:gocritic // hugeParam: cfg mirrors the driver interface's value semantics.
func applyTargetMask(job *driver.Job, cfg driver.JobConfig, mask []string) {
	if maskHas(mask, "httpTarget") {
		job.HTTPTarget = cloneHTTPTarget(cfg.HTTPTarget)
	}

	if maskHas(mask, "pubsubTarget") {
		job.PubsubTarget = clonePubsubTarget(cfg.PubsubTarget)
	}

	if maskHas(mask, "appEngineHttpTarget") {
		job.AppEngineHTTPTarget = cloneAppEngineTarget(cfg.AppEngineHTTPTarget)
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
