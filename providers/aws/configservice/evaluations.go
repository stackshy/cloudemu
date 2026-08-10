package configservice

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

// PutEvaluations records evaluations reported by a custom rule and rolls the
// rule's aggregate compliance up from them. The resultToken identifies the
// rule (the emulator treats it as the rule name for local reporting). Returns
// any evaluations that failed (always empty here). In testMode nothing is
// persisted (real Config behavior).
func (m *Mock) PutEvaluations(
	_ context.Context, resultToken string, evals []driver.Evaluation, testMode bool,
) ([]driver.Evaluation, error) {
	if resultToken == "" {
		return nil, invalidParameter("ResultToken is required")
	}

	if testMode {
		return nil, nil
	}

	rd, ok := m.rules.Get(resultToken)
	if !ok {
		// The resultToken maps to a rule name in the emulator; an unknown token
		// is a NoSuchConfigRule so callers see a precise error.
		return nil, noSuchConfigRule(resultToken)
	}

	rd.mu.Lock()
	rd.evals = append(rd.evals, evals...)
	rd.rule.Compliance = rollUpCompliance(rd.evals)
	rd.rule.LastSuccessfulEval = m.now()
	rd.mu.Unlock()

	return nil, nil
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
	rd, ok := m.rules.Get(ruleName)
	if !ok {
		return noSuchConfigRule(ruleName)
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
	_ context.Context, _ []string,
) (compliant, nonCompliant int32, err error) {
	// Synthesized: the emulator reports the compliance from recorded evaluations.
	for _, k := range sortedKeys(m.rules.Keys()) {
		rd, ok := m.rules.Get(k)
		if !ok {
			continue
		}

		rd.mu.RLock()
		for _, e := range rd.evals {
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
