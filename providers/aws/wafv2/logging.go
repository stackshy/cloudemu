package wafv2

import (
	"context"
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/services/wafv2/driver"
)

// loggingConfigData holds a stored LoggingConfiguration keyed by ResourceArn.
// The configuration is stored verbatim as raw JSON so Get/List return exactly
// what Put wrote, and scope is remembered so ListLoggingConfigurations can
// filter by scope (a REGIONAL vs CLOUDFRONT web ACL ARN).
type loggingConfigData struct {
	scope string
	cfg   json.RawMessage
}

// resourceArnField extracts the ResourceArn from a raw LoggingConfiguration.
func resourceArnField(cfg json.RawMessage) string {
	var probe struct {
		ResourceArn string `json:"ResourceArn"`
	}

	_ = json.Unmarshal(cfg, &probe)

	return probe.ResourceArn
}

// scopeOfARN returns the scope of the web ACL identified by arn, defaulting to
// REGIONAL when the ARN doesn't match a stored web ACL.
func (m *Mock) scopeOfARN(arn string) string {
	if wd, ok := m.webACLByARN(arn); ok {
		wd.mu.RLock()
		defer wd.mu.RUnlock()

		return wd.acl.Scope
	}

	return driver.ScopeRegional
}

// PutLoggingConfiguration stores a LoggingConfiguration keyed by its ResourceArn
// and echoes it back, matching WAF's create-or-replace semantics.
func (m *Mock) PutLoggingConfiguration(_ context.Context, cfg json.RawMessage) (json.RawMessage, error) {
	arn := resourceArnField(cfg)
	if arn == "" {
		return nil, invalidParameter("LoggingConfiguration.ResourceArn is required")
	}

	stored := copyBytes(cfg)
	m.logCfgs.Set(arn, &loggingConfigData{scope: m.scopeOfARN(arn), cfg: stored})

	return copyBytes(stored), nil
}

// GetLoggingConfiguration returns the LoggingConfiguration for a resource ARN.
func (m *Mock) GetLoggingConfiguration(_ context.Context, resourceARN string) (json.RawMessage, error) {
	lc, ok := m.logCfgs.Get(resourceARN)
	if !ok {
		return nil, nonexistent("no logging configuration for %q", resourceARN)
	}

	return copyBytes(lc.cfg), nil
}

// DeleteLoggingConfiguration removes the LoggingConfiguration for a resource ARN.
func (m *Mock) DeleteLoggingConfiguration(_ context.Context, resourceARN string) error {
	if _, ok := m.logCfgs.Get(resourceARN); !ok {
		return nonexistent("no logging configuration for %q", resourceARN)
	}

	m.logCfgs.Delete(resourceARN)

	return nil
}

// ListLoggingConfigurations returns every stored LoggingConfiguration in a scope.
func (m *Mock) ListLoggingConfigurations(_ context.Context, scope string) ([]json.RawMessage, error) {
	all := m.logCfgs.All()
	out := make([]json.RawMessage, 0, len(all))

	for _, lc := range all {
		if lc.scope == scope {
			out = append(out, copyBytes(lc.cfg))
		}
	}

	return out, nil
}
