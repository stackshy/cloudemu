package configservice

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

// issueResultToken generates a fresh opaque result token for a rule and records
// the token -> rule-name mapping in the registry, dropping the rule's previous
// token. The caller must NOT hold the rule's lock.
func (m *Mock) issueResultToken(ruleName string) string {
	token := idgen.GenerateID("config-result-token-")

	m.tokenMu.Lock()
	defer m.tokenMu.Unlock()

	m.evalTokens[token] = ruleName

	return token
}

// ResultTokenForRule returns the opaque result token currently issued for a
// rule. It is exported for provider tests (real Config delivers the token to a
// custom rule's Lambda; the SDK never surfaces it) so PutEvaluations can be
// exercised with a valid token.
func (m *Mock) ResultTokenForRule(ruleName string) (string, bool) {
	rd, ok := m.rules.Get(ruleName)
	if !ok {
		return "", false
	}

	rd.mu.RLock()
	defer rd.mu.RUnlock()

	if rd.resultToken == "" {
		return "", false
	}

	return rd.resultToken, true
}

// PutEvaluations records evaluations reported by a custom rule and rolls the
// rule's aggregate compliance up from them. The resultToken is a large opaque
// token the emulator issued for a rule (at create time or via
// StartConfigRulesEvaluation) — never the rule name. An unknown or malformed
// token is an InvalidResultTokenException. Returns any evaluations that failed
// (always empty here). In testMode nothing is persisted (real Config behavior).
func (m *Mock) PutEvaluations(
	_ context.Context, resultToken string, evals []driver.Evaluation, testMode bool,
) ([]driver.Evaluation, error) {
	if resultToken == "" {
		return nil, invalidResultToken(resultToken)
	}

	m.tokenMu.RLock()
	ruleName, known := m.evalTokens[resultToken]
	m.tokenMu.RUnlock()

	if !known {
		return nil, invalidResultToken(resultToken)
	}

	if err := validateEvaluations(evals); err != nil {
		return nil, err
	}

	if testMode {
		return nil, nil
	}

	rd, ok := m.rules.Get(ruleName)
	if !ok {
		// The rule was deleted after the token was issued.
		return nil, invalidResultToken(resultToken)
	}

	now := m.now()

	for i := range evals {
		if evals[i].OrderingTimestamp.IsZero() {
			evals[i].OrderingTimestamp = now
		}
	}

	rd.mu.Lock()
	rd.evals = append(rd.evals, evals...)
	rd.rule.Compliance = rollUpCompliance(rd.evals)
	rd.rule.LastSuccessfulEval = now
	rd.mu.Unlock()

	return nil, nil
}

// validateEvaluations validates each reported evaluation's ComplianceType.
func validateEvaluations(evals []driver.Evaluation) error {
	for i := range evals {
		if err := validateComplianceType(evals[i].ComplianceType); err != nil {
			return err
		}
	}

	return nil
}

// validateComplianceType rejects a compliance value outside the allowed set.
func validateComplianceType(ct string) error {
	switch ct {
	case driver.ComplianceCompliant, driver.ComplianceNonCompliant,
		driver.ComplianceNotApplicable, driver.ComplianceInsufficientData:
		return nil
	default:
		return invalidParameter("ComplianceType %q is invalid", ct)
	}
}

// rollUpCompliance derives a rule's aggregate compliance from its evaluations:
// any NON_COMPLIANT wins, else COMPLIANT if any compliant, else insufficient.
func rollUpCompliance(evals []driver.Evaluation) string {
	if len(evals) == 0 {
		return driver.ComplianceInsufficientData
	}

	hasCompliant := false

	for _, e := range evals {
		switch e.ComplianceType {
		case driver.ComplianceNonCompliant:
			return driver.ComplianceNonCompliant
		case driver.ComplianceCompliant:
			hasCompliant = true
		}
	}

	if hasCompliant {
		return driver.ComplianceCompliant
	}

	return driver.ComplianceNotApplicable
}

// PutExternalEvaluation records a single external evaluation against a rule.
//
//nolint:gocritic // eval is passed by value to match the driver.Config interface signature.
func (m *Mock) PutExternalEvaluation(_ context.Context, ruleName string, eval driver.Evaluation) error {
	if err := validateComplianceType(eval.ComplianceType); err != nil {
		return err
	}

	rd, ok := m.rules.Get(ruleName)
	if !ok {
		return noSuchConfigRule(ruleName)
	}

	if eval.OrderingTimestamp.IsZero() {
		eval.OrderingTimestamp = m.now()
	}

	rd.mu.Lock()
	rd.evals = append(rd.evals, eval)
	rd.rule.Compliance = rollUpCompliance(rd.evals)
	rd.mu.Unlock()

	return nil
}

// DeleteEvaluationResults clears a rule's recorded evaluations.
func (m *Mock) DeleteEvaluationResults(_ context.Context, ruleName string) error {
	rd, ok := m.rules.Get(ruleName)
	if !ok {
		return noSuchConfigRule(ruleName)
	}

	rd.mu.Lock()
	rd.evals = nil
	rd.rule.Compliance = driver.ComplianceInsufficientData
	rd.mu.Unlock()

	return nil
}

// DescribeComplianceByConfigRule returns rules with their aggregate compliance
// (synthesized from reported evaluations).
func (m *Mock) DescribeComplianceByConfigRule(
	ctx context.Context, names []string, page driver.Page,
) ([]driver.ConfigRule, string, error) {
	return m.DescribeConfigRules(ctx, names, page)
}

// DescribeComplianceByResource returns compliance for a resource. Synthesized:
// returns the PutResourceConfig item(s) matching the type/id, if any.
func (m *Mock) DescribeComplianceByResource(
	_ context.Context, resourceType, resourceID string, page driver.Page,
) ([]driver.ConfigurationItem, string, error) {
	items := m.matchingResources(resourceType, resourceID)

	return paginate(items, page)
}

// GetComplianceDetailsByConfigRule returns the per-resource evaluations recorded
// for a rule.
func (m *Mock) GetComplianceDetailsByConfigRule(
	_ context.Context, ruleName string, page driver.Page,
) ([]driver.Evaluation, string, error) {
	rd, ok := m.rules.Get(ruleName)
	if !ok {
		return nil, "", noSuchConfigRule(ruleName)
	}

	rd.mu.RLock()
	evals := append([]driver.Evaluation(nil), rd.evals...)
	rd.mu.RUnlock()

	return paginate(evals, page)
}

// GetComplianceDetailsByResource returns evaluations for a specific resource
// across all rules (synthesized by filtering recorded evaluations).
func (m *Mock) GetComplianceDetailsByResource(
	_ context.Context, resourceType, resourceID string, page driver.Page,
) ([]driver.Evaluation, string, error) {
	var out []driver.Evaluation

	for _, k := range sortedKeys(m.rules.Keys()) {
		rd, ok := m.rules.Get(k)
		if !ok {
			continue
		}

		rd.mu.RLock()
		for _, e := range rd.evals {
			if e.ComplianceResourceType == resourceType && e.ComplianceResourceID == resourceID {
				out = append(out, e)
			}
		}
		rd.mu.RUnlock()
	}

	return paginate(out, page)
}

// GetComplianceSummaryByConfigRule tallies compliant vs non-compliant rules.
func (m *Mock) GetComplianceSummaryByConfigRule(_ context.Context) (compliant, nonCompliant int32, err error) {
	rules := m.allRules()
	for i := range rules {
		switch rules[i].Compliance {
		case driver.ComplianceCompliant:
			compliant++
		case driver.ComplianceNonCompliant:
			nonCompliant++
		}
	}

	return compliant, nonCompliant, nil
}

// GetComplianceSummaryByResourceType tallies compliant vs non-compliant
// resources (synthesized from recorded PutResourceConfig items — all counted as
// compliant unless an evaluation marks them otherwise).
func (m *Mock) GetComplianceSummaryByResourceType(
	_ context.Context, resourceTypes []string,
) (compliant, nonCompliant int32, err error) {
	want := map[string]bool{}
	for _, t := range resourceTypes {
		want[t] = true
	}

	// Synthesized: the emulator reports the compliance from recorded evaluations,
	// filtered to the requested resource types when any are supplied.
	for _, k := range sortedKeys(m.rules.Keys()) {
		rd, ok := m.rules.Get(k)
		if !ok {
			continue
		}

		rd.mu.RLock()
		for _, e := range rd.evals {
			if len(want) > 0 && !want[e.ComplianceResourceType] {
				continue
			}

			if e.ComplianceType == driver.ComplianceNonCompliant {
				nonCompliant++
			} else if e.ComplianceType == driver.ComplianceCompliant {
				compliant++
			}
		}
		rd.mu.RUnlock()
	}

	return compliant, nonCompliant, nil
}

// StartResourceEvaluation starts an on-demand evaluation, returning a synthesized
// evaluation ID.
func (*Mock) StartResourceEvaluation(_ context.Context, resourceType, _ string) (string, error) {
	if resourceType == "" {
		return "", invalidParameter("resourceType is required")
	}

	return idgen.GenerateID("resource-evaluation-"), nil
}

// GetResourceEvaluationSummary returns a synthesized COMPLIANT summary for an
// evaluation ID.
func (*Mock) GetResourceEvaluationSummary(
	_ context.Context, evaluationID string,
) (status, resourceType string, err error) {
	if evaluationID == "" {
		return "", "", invalidParameter("ResourceEvaluationId is required")
	}

	return "SUCCEEDED", "AWS::EC2::Instance", nil
}

// ListResourceEvaluations returns recorded evaluation IDs (synthesized empty).
func (*Mock) ListResourceEvaluations(_ context.Context, page driver.Page) (ids []string, nextToken string, err error) {
	return paginate([]string{}, page)
}
