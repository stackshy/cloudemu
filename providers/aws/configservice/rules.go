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

	if err := validateRuleSource(rule.Source); err != nil {
		return err
	}

	if err := validateMaxExecutionFrequency(rule.MaximumExecutionFrequency); err != nil {
		return err
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

	// Cap creates under createMu so the limit check + insert is atomic and the
	// per-account rule cap can't be raced past.
	m.createMu.Lock()
	defer m.createMu.Unlock()

	// Re-check under the lock: a concurrent create of the SAME name must be an
	// idempotent upsert, never a ResourceInUseException (real Config semantics).
	if existing, ok := m.rules.Get(rule.ConfigRuleName); ok {
		existing.mu.Lock()
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

	if m.rules.Len() >= maxConfigRules {
		return tagged(driver.ExMaxNumberOfConfigRulesExceeded, failedPreconditionCode,
			"an account supports at most %d config rules", maxConfigRules)
	}

	rule.ConfigRuleID = idgen.GenerateID("config-rule-")
	rule.ConfigRuleArn = m.arn("config-rule/" + rule.ConfigRuleID)
	rule.ConfigRuleState = driver.RuleStateActive
	rule.Compliance = driver.ComplianceInsufficientData
	rule.Tags = copyTags(rule.Tags)

	rd := &ruleData{rule: rule, resultToken: m.issueResultToken(rule.ConfigRuleName)}
	m.rules.Set(rule.ConfigRuleName, rd)

	return nil
}

// validateRuleSource validates a rule's Source: Owner must be one of the known
// values, and a managed rule requires a SourceIdentifier.
func validateRuleSource(src *driver.RuleSource) error {
	if src == nil || src.Owner == "" {
		return invalidParameter("Source.Owner is required")
	}

	switch src.Owner {
	case "AWS":
		if src.SourceIdentifier == "" {
			return invalidParameter("Source.SourceIdentifier is required for AWS-managed rules")
		}
	case "CUSTOM_LAMBDA":
		if src.SourceIdentifier == "" {
			return invalidParameter("Source.SourceIdentifier is required for CUSTOM_LAMBDA rules")
		}
	case "CUSTOM_POLICY":
		// Custom-policy rules carry PolicyText rather than a SourceIdentifier.
	default:
		return invalidParameter("Source.Owner %q is invalid (want AWS, CUSTOM_LAMBDA or CUSTOM_POLICY)", src.Owner)
	}

	return nil
}

// validateMaxExecutionFrequency validates a MaximumExecutionFrequency value if
// present; an empty value is allowed (event-triggered rules).
func validateMaxExecutionFrequency(freq string) error {
	if freq == "" {
		return nil
	}

	switch freq {
	case "One_Hour", "Three_Hours", "Six_Hours", "Twelve_Hours", "TwentyFour_Hours":
		return nil
	default:
		return invalidParameter("MaximumExecutionFrequency %q is invalid", freq)
	}
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

	// Drop any result tokens issued for the deleted rule so stale tokens can't be
	// replayed against a recreated same-named rule.
	m.tokenMu.Lock()
	for token, rn := range m.evalTokens {
		if rn == name {
			delete(m.evalTokens, token)
		}
	}
	m.tokenMu.Unlock()

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

// StartConfigRulesEvaluation triggers evaluation of the named rules. Real Config
// dispatches a fresh opaque result token to each rule's evaluator; the emulator
// issues a new token per named rule (validated before any mutation). Evaluations
// then arrive via PutEvaluations carrying that token.
func (m *Mock) StartConfigRulesEvaluation(_ context.Context, names []string) error {
	for _, n := range names {
		if !m.rules.Has(n) {
			return noSuchConfigRule(n)
		}
	}

	for _, n := range names {
		rd, ok := m.rules.Get(n)
		if !ok {
			continue
		}

		token := m.issueResultToken(n)

		rd.mu.Lock()
		rd.resultToken = token
		rd.mu.Unlock()
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
