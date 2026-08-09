package wafv2

import "context"

// PutPermissionPolicy stores an IAM policy JSON string per rule-group ARN,
// granting cross-account use of the rule group. WAF requires the target to be a
// rule group; the emulator verifies the ARN belongs to a stored rule group.
func (m *Mock) PutPermissionPolicy(_ context.Context, resourceARN, policy string) error {
	if resourceARN == "" || policy == "" {
		return invalidParameter("ResourceArn and Policy are required")
	}

	if _, ok := m.ruleGroupByARN(resourceARN); !ok {
		return nonexistent("rule group %q not found", resourceARN)
	}

	m.policyMu.Lock()
	defer m.policyMu.Unlock()

	m.policies[resourceARN] = policy

	return nil
}

// GetPermissionPolicy returns the policy stored for a rule-group ARN.
func (m *Mock) GetPermissionPolicy(_ context.Context, resourceARN string) (string, error) {
	m.policyMu.RLock()
	defer m.policyMu.RUnlock()

	policy, ok := m.policies[resourceARN]
	if !ok {
		return "", nonexistent("no permission policy for %q", resourceARN)
	}

	return policy, nil
}

// DeletePermissionPolicy removes the policy stored for a rule-group ARN.
func (m *Mock) DeletePermissionPolicy(_ context.Context, resourceARN string) error {
	m.policyMu.Lock()
	defer m.policyMu.Unlock()

	if _, ok := m.policies[resourceARN]; !ok {
		return nonexistent("no permission policy for %q", resourceARN)
	}

	delete(m.policies, resourceARN)

	return nil
}

// ruleGroupByARN finds a stored rule group by its ARN.
func (m *Mock) ruleGroupByARN(arn string) (*ruleGroupData, bool) {
	for _, gd := range m.ruleGrps.All() {
		gd.mu.RLock()
		match := gd.grp.ARN == arn
		gd.mu.RUnlock()

		if match {
			return gd, true
		}
	}

	return nil, false
}
