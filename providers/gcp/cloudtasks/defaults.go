package cloudtasks

import "github.com/stackshy/cloudemu/v2/services/cloudtasks/driver"

// Queue defaults, applied on create/patch so a Get always returns the canonical
// fully-populated form a real Cloud Tasks server fills in. Cloud Tasks always
// emits complete rateLimits and retryConfig blocks (unlike Cloud Scheduler,
// which omits an unset retryConfig), so both blocks are synthesized here when a
// queue does not carry one.
const (
	defaultMaxDispatchesPerSecond  = 500.0
	defaultMaxConcurrentDispatches = 1000
	defaultMaxAttempts             = 100
	defaultMaxRetryDuration        = "0s"
	defaultMinBackoff              = "0.100s"
	defaultMaxBackoff              = "3600s"
	defaultMaxDoublings            = 16
)

// maxBurstSize step-table breakpoints. maxBurstSize is output-only and computed
// from maxDispatchesPerSecond by real Cloud Tasks via a token-bucket step
// function that Google does not publicly document. The one pinned anchor is the
// service default: maxDispatchesPerSecond=500 yields maxBurstSize=100 (as
// reported by `gcloud tasks queues describe`). This monotonic step function
// reproduces that anchor; since the field is output-only and Terraform's google
// provider treats max_burst_size as Computed, the exact non-default values are
// not a drift source — only stability across a Get round-trip matters.
const (
	burstStepLow    = 1.0
	burstStepMid    = 10.0
	burstStepHigh   = 100.0
	burstSizeTiny   = 10
	burstSizeLow    = 20
	burstSizeMid    = 50
	burstSizeMax    = 100
	burstSizeNoRate = 0
)

// applyDefaults fills the output defaults a real Cloud Tasks server returns,
// always leaving the queue with fully-populated rateLimits and retryConfig.
func applyDefaults(q *driver.Queue) {
	applyRateLimitDefaults(q)
	applyRetryDefaults(q)
}

// applyRateLimitDefaults fills the rateLimits block, defaulting the dispatch
// rate and concurrency and (re)computing the output-only maxBurstSize.
func applyRateLimitDefaults(q *driver.Queue) {
	if q.RateLimits == nil {
		q.RateLimits = &driver.RateLimits{}
	}

	rl := q.RateLimits
	if rl.MaxDispatchesPerSecond == 0 {
		rl.MaxDispatchesPerSecond = defaultMaxDispatchesPerSecond
	}

	if rl.MaxConcurrentDispatches == 0 {
		rl.MaxConcurrentDispatches = defaultMaxConcurrentDispatches
	}

	rl.MaxBurstSize = computeMaxBurstSize(rl.MaxDispatchesPerSecond)
}

// applyRetryDefaults fills the retryConfig block, completing each unset
// sub-field. maxAttempts 0 means unset (0 is not a valid attempt count; -1 is
// the valid "unlimited" sentinel and is preserved).
func applyRetryDefaults(q *driver.Queue) {
	if q.RetryConfig == nil {
		q.RetryConfig = &driver.RetryConfig{}
	}

	rc := q.RetryConfig
	if rc.MaxAttempts == 0 {
		rc.MaxAttempts = defaultMaxAttempts
	}

	if rc.MaxRetryDuration == "" {
		rc.MaxRetryDuration = defaultMaxRetryDuration
	}

	if rc.MinBackoff == "" {
		rc.MinBackoff = defaultMinBackoff
	}

	if rc.MaxBackoff == "" {
		rc.MaxBackoff = defaultMaxBackoff
	}

	if rc.MaxDoublings == 0 {
		rc.MaxDoublings = defaultMaxDoublings
	}
}

// computeMaxBurstSize approximates Cloud Tasks' token-bucket step function,
// anchored on the documented default (dps=500 → 100).
func computeMaxBurstSize(dps float64) int64 {
	switch {
	case dps <= 0:
		return burstSizeNoRate
	case dps < burstStepLow:
		return burstSizeTiny
	case dps < burstStepMid:
		return burstSizeLow
	case dps < burstStepHigh:
		return burstSizeMid
	default: // dps >= 100, including the default 500
		return burstSizeMax
	}
}

// queueFromConfig materializes a Queue from a create config (deep-copying the
// nested config blocks).
func queueFromConfig(cfg driver.QueueConfig) *driver.Queue {
	return &driver.Queue{
		Name:                     cfg.Name,
		AppEngineRoutingOverride: cloneAppEngineRouting(cfg.AppEngineRoutingOverride),
		RateLimits:               cloneRateLimits(cfg.RateLimits),
		RetryConfig:              cloneRetryConfig(cfg.RetryConfig),
		StackdriverLoggingConfig: cloneLoggingConfig(cfg.StackdriverLoggingConfig),
		HTTPTarget:               cloneBytes(cfg.HTTPTarget),
	}
}
