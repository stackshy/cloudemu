package configservice

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

// PutRemediationConfigurations attaches remediation to config rules. It
// validates every entry (each rule must exist) BEFORE mutating any, so a bad
// entry in the batch never partially applies.
func (m *Mock) PutRemediationConfigurations(
	_ context.Context, cfgs []driver.RemediationConfiguration,
) ([]driver.RemediationConfiguration, error) {
	if len(cfgs) == 0 {
		return nil, invalidParameter("RemediationConfigurations must not be empty")
	}

	for i := range cfgs {
		if cfgs[i].ConfigRuleName == "" {
			return nil, invalidParameter("ConfigRuleName is required")
		}

		if !m.rules.Has(cfgs[i].ConfigRuleName) {
			return nil, noSuchConfigRule(cfgs[i].ConfigRuleName)
		}

		if cfgs[i].TargetID == "" {
			return nil, invalidParameter("TargetId is required for rule %q", cfgs[i].ConfigRuleName)
		}
	}

	for i := range cfgs {
		cfg := cfgs[i]
		cfg.Arn = m.arn("remediation-configuration/" + cfg.ConfigRuleName)
		cfg.Parameters = copyTags(cfg.Parameters)
		m.remediation.Set(cfg.ConfigRuleName, &cfg)
	}

	return nil, nil // FailedBatches is empty on full success
}

// DescribeRemediationConfigurations returns remediation for the named rules.
func (m *Mock) DescribeRemediationConfigurations(
	_ context.Context, ruleNames []string,
) ([]driver.RemediationConfiguration, error) {
	out := make([]driver.RemediationConfiguration, 0, len(ruleNames))

	for _, name := range ruleNames {
		cfg, ok := m.remediation.Get(name)
		if !ok {
			continue
		}

		cp := *cfg
		cp.Parameters = copyTags(cfg.Parameters)
		out = append(out, cp)
	}

	return out, nil
}

// DeleteRemediationConfiguration removes remediation for a rule.
func (m *Mock) DeleteRemediationConfiguration(_ context.Context, ruleName, _ string) error {
	if !m.remediation.Delete(ruleName) {
		return noSuchRemediationConfig(ruleName)
	}

	return nil
}

// PutRemediationExceptions suppresses remediation for specific resources of a
// rule. Validates the rule exists before mutating.
func (m *Mock) PutRemediationExceptions(
	_ context.Context, ruleName string, exceptions []driver.RemediationException,
) ([]driver.RemediationException, error) {
	if !m.rules.Has(ruleName) {
		return nil, noSuchConfigRule(ruleName)
	}

	if len(exceptions) == 0 {
		return nil, invalidParameter("ResourceKeys must not be empty")
	}

	m.authMu.Lock()

	for i := range exceptions {
		exceptions[i].ConfigRuleName = ruleName
	}

	m.remExceptions[ruleName] = append(m.remExceptions[ruleName], exceptions...)
	m.authMu.Unlock()

	return nil, nil // FailedBatches empty on success
}

// DescribeRemediationExceptions lists a rule's remediation exceptions.
func (m *Mock) DescribeRemediationExceptions(
	_ context.Context, ruleName string, keys []driver.ResourceKey, page driver.Page,
) ([]driver.RemediationException, string, error) {
	m.authMu.RLock()
	src := append([]driver.RemediationException(nil), m.remExceptions[ruleName]...)
	m.authMu.RUnlock()

	if len(keys) > 0 {
		src = filterExceptions(src, keys)
	}

	return paginate(src, page)
}

func filterExceptions(src []driver.RemediationException, keys []driver.ResourceKey) []driver.RemediationException {
	set := map[string]bool{}
	for _, k := range keys {
		set[resourceKey(k.ResourceType, k.ResourceID)] = true
	}

	out := make([]driver.RemediationException, 0, len(src))

	for _, e := range src {
		if set[resourceKey(e.ResourceType, e.ResourceID)] {
			out = append(out, e)
		}
	}

	return out
}

// DeleteRemediationExceptions removes remediation exceptions, returning any keys
// that could not be found (real Config's FailedBatches).
func (m *Mock) DeleteRemediationExceptions(
	_ context.Context, ruleName string, keys []driver.ResourceKey,
) ([]driver.ResourceKey, error) {
	if len(keys) == 0 {
		return nil, invalidParameter("ResourceKeys must not be empty")
	}

	m.authMu.Lock()
	defer m.authMu.Unlock()

	existing := m.remExceptions[ruleName]
	remaining := existing[:0:0]
	del := map[string]bool{}

	for _, k := range keys {
		del[resourceKey(k.ResourceType, k.ResourceID)] = true
	}

	deleted := map[string]bool{}

	for _, e := range existing {
		key := resourceKey(e.ResourceType, e.ResourceID)
		if del[key] {
			deleted[key] = true
			continue
		}

		remaining = append(remaining, e)
	}

	m.remExceptions[ruleName] = remaining

	var failed []driver.ResourceKey

	for _, k := range keys {
		if !deleted[resourceKey(k.ResourceType, k.ResourceID)] {
			failed = append(failed, k)
		}
	}

	return failed, nil
}

// DescribeRemediationExecutionStatus returns per-resource execution status.
// Synthesized empty (no remediation runs in the emulator).
func (m *Mock) DescribeRemediationExecutionStatus(
	_ context.Context, ruleName string, _ []driver.ResourceKey, page driver.Page,
) ([]driver.ResourceKey, string, error) {
	if !m.remediation.Has(ruleName) {
		return nil, "", noSuchRemediationConfig(ruleName)
	}

	return paginate([]driver.ResourceKey{}, page)
}

// StartRemediationExecution starts remediation for resources. Validates the rule
// has a remediation configuration; returns the accepted keys.
func (m *Mock) StartRemediationExecution(
	_ context.Context, ruleName string, keys []driver.ResourceKey,
) ([]driver.ResourceKey, error) {
	if !m.remediation.Has(ruleName) {
		return nil, noSuchRemediationConfig(ruleName)
	}

	if len(keys) == 0 {
		return nil, invalidParameter("ResourceKeys must not be empty")
	}

	// All keys are accepted; none fail in the emulator.
	return nil, nil
}
