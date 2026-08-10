package configservice

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

// PutConfigRule creates or updates a config rule (upsert semantics matching real
// Config). A new rule is assigned an ARN, ID, and ACTIVE state; its compliance
// starts as INSUFFICIENT_DATA until an evaluation is reported.
//
//nolint:gocritic // rule is the driver ConfigRule input, taken by value to match the driver API.
func (m *Mock) PutConfigRule(_ context.Context, rule driver.ConfigRule) error {
	if rule.ConfigRuleName == "" {
		return invalidParameter("ConfigRuleName is required")
	}

	if rule.Source == nil || rule.Source.Owner == "" {
		return invalidParameter("Source.Owner is required")
	}

	if existing, ok := m.rules.Get(rule.ConfigRuleName); ok {
		existing.mu.Lock()
		// Preserve the identity fields; update the mutable config.
		rule.ConfigRuleArn = existing.rule.ConfigRuleArn
		rule.ConfigRuleID = existing.rule.ConfigRuleID
		rule.CreatedBy = existing.rule.CreatedBy
		rule.ConfigRuleState = driver.RuleStateActive
		rule.Compliance = existing.rule.Compliance
		rule.Tags = copyTags(rule.Tags)
		existing.rule = rule
		existing.mu.Unlock()

		return nil
	}

	rule.ConfigRuleID = idgen.GenerateID("config-rule-")
	rule.ConfigRuleArn = m.arn("config-rule/" + rule.ConfigRuleID)
	rule.ConfigRuleState = driver.RuleStateActive
	rule.Compliance = driver.ComplianceInsufficientData
	rule.Tags = copyTags(rule.Tags)

	if !m.rules.SetIfAbsent(rule.ConfigRuleName, &ruleData{rule: rule}) {
		return resourceInUse("config rule %q already exists", rule.ConfigRuleName)
	}

	return nil
}

func copyRule(r *driver.ConfigRule) driver.ConfigRule {
	out := *r
	out.Tags = copyTags(r.Tags)

	if r.Scope != nil {
		s := *r.Scope
		s.ComplianceResourceTypes = copyStrings(r.Scope.ComplianceResourceTypes)
		out.Scope = &s
	}

	if r.Source != nil {
		src := *r.Source
		out.Source = &src
	}

	return out
}

func (m *Mock) allRules() []driver.ConfigRule {
	keys := sortedKeys(m.rules.Keys())
	out := make([]driver.ConfigRule, 0, len(keys))

	for _, k := range keys {
		rd, ok := m.rules.Get(k)
		if !ok {
			continue
		}

		rd.mu.RLock()
		out = append(out, copyRule(&rd.rule))
		rd.mu.RUnlock()
	}

	return out
}

// DescribeConfigRules returns the named rules (all if empty), paginated. A
// named-but-absent rule is a NoSuchConfigRuleException.
func (m *Mock) DescribeConfigRules(
	_ context.Context, names []string, page driver.Page,
) ([]driver.ConfigRule, string, error) {
	for _, n := range names {
		if !m.rules.Has(n) {
			return nil, "", noSuchConfigRule(n)
		}
	}

	filtered := filterByNames(m.allRules(), func(r driver.ConfigRule) string { return r.ConfigRuleName }, names)

	return paginate(filtered, page)
}

// DeleteConfigRule removes a rule and any attached remediation configuration.
func (m *Mock) DeleteConfigRule(_ context.Context, name string) error {
	if !m.rules.Delete(name) {
		return noSuchConfigRule(name)
	}

	// Release the dependent remediation config and exceptions so they aren't
	// orphaned.
	m.remediation.Delete(name)
	m.authMu.Lock()
	delete(m.remExceptions, name)
	m.authMu.Unlock()

	return nil
}

// DescribeConfigRuleEvaluationStatus returns per-rule evaluation status (all if
// empty), paginated.
func (m *Mock) DescribeConfigRuleEvaluationStatus(
	_ context.Context, names []string, page driver.Page,
) ([]driver.ConfigRule, string, error) {
	for _, n := range names {
		if !m.rules.Has(n) {
			return nil, "", noSuchConfigRule(n)
		}
	}

	filtered := filterByNames(m.allRules(), func(r driver.ConfigRule) string { return r.ConfigRuleName }, names)

	return paginate(filtered, page)
}

// StartConfigRulesEvaluation triggers evaluation of the named rules. In the
// emulator this is a validated no-op (evaluations arrive via PutEvaluations).
func (m *Mock) StartConfigRulesEvaluation(_ context.Context, names []string) error {
	for _, n := range names {
		if !m.rules.Has(n) {
			return noSuchConfigRule(n)
		}
	}

	return nil
}

// GetCustomRulePolicy returns the Guard policy text of a custom-policy rule.
func (m *Mock) GetCustomRulePolicy(_ context.Context, ruleName string) (string, error) {
	rd, ok := m.rules.Get(ruleName)
	if !ok {
		return "", noSuchConfigRule(ruleName)
	}

	rd.mu.RLock()
	defer rd.mu.RUnlock()

	if rd.rule.Source != nil {
		return rd.rule.Source.PolicyText, nil
	}

	return "", nil
}
