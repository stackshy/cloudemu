package cloudtasks

import "github.com/stackshy/cloudemu/v2/services/cloudtasks/driver"

// cloneQueue returns a deep copy of a queue so the store never hands out a
// pointer into its own state (copy-on-write at the driver boundary).
func cloneQueue(q *driver.Queue) *driver.Queue {
	cp := *q
	cp.AppEngineRoutingOverride = cloneAppEngineRouting(q.AppEngineRoutingOverride)
	cp.RateLimits = cloneRateLimits(q.RateLimits)
	cp.RetryConfig = cloneRetryConfig(q.RetryConfig)
	cp.StackdriverLoggingConfig = cloneLoggingConfig(q.StackdriverLoggingConfig)
	cp.HTTPTarget = cloneBytes(q.HTTPTarget)
	cp.IAMPolicy = clonePolicy(q.IAMPolicy)

	return &cp
}

func cloneAppEngineRouting(in *driver.AppEngineRouting) *driver.AppEngineRouting {
	if in == nil {
		return nil
	}

	cp := *in

	return &cp
}

func cloneRateLimits(in *driver.RateLimits) *driver.RateLimits {
	if in == nil {
		return nil
	}

	cp := *in

	return &cp
}

func cloneRetryConfig(in *driver.RetryConfig) *driver.RetryConfig {
	if in == nil {
		return nil
	}

	cp := *in

	return &cp
}

func cloneLoggingConfig(in *driver.StackdriverLoggingConfig) *driver.StackdriverLoggingConfig {
	if in == nil {
		return nil
	}

	cp := *in

	return &cp
}

// clonePolicy deep-copies an IAM policy so stored and returned values don't
// share backing slices.
func clonePolicy(p *driver.IAMPolicy) *driver.IAMPolicy {
	if p == nil {
		return nil
	}

	out := &driver.IAMPolicy{Version: p.Version, Etag: p.Etag}
	for _, b := range p.Bindings {
		out.Bindings = append(out.Bindings, driver.IAMBinding{Role: b.Role, Members: cloneStrings(b.Members)})
	}

	return out
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}

	out := make([]string, len(in))
	copy(out, in)

	return out
}

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}

	out := make([]byte, len(in))
	copy(out, in)

	return out
}
