package cloudtrail

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
)

// PutResourcePolicy stores a resource-based policy for a channel or event data
// store ARN.
func (m *Mock) PutResourcePolicy(
	_ context.Context, resourceARN, policy string,
) (outARN, outPolicy string, err error) {
	if resourceARN == "" {
		return "", "", errInvalidParameter("ResourceArn is required")
	}

	m.policyMu.Lock()
	defer m.policyMu.Unlock()

	m.policies[resourceARN] = policy

	return resourceARN, policy, nil
}

// GetResourcePolicy returns a resource's policy.
func (m *Mock) GetResourcePolicy(
	_ context.Context, resourceARN string,
) (outARN, policy string, err error) {
	m.policyMu.RLock()
	defer m.policyMu.RUnlock()

	policy, ok := m.policies[resourceARN]
	if !ok {
		return "", "", errResourcePolicyNotFound(resourceARN)
	}

	return resourceARN, policy, nil
}

// DeleteResourcePolicy removes a resource's policy.
func (m *Mock) DeleteResourcePolicy(_ context.Context, resourceARN string) error {
	m.policyMu.Lock()
	defer m.policyMu.Unlock()

	if _, ok := m.policies[resourceARN]; !ok {
		return errResourcePolicyNotFound(resourceARN)
	}

	delete(m.policies, resourceARN)

	return nil
}

// RegisterOrganizationDelegatedAdmin records a delegated administrator account.
func (m *Mock) RegisterOrganizationDelegatedAdmin(_ context.Context, memberAccountID string) error {
	if memberAccountID == "" {
		return errInvalidParameter("MemberAccountId is required")
	}

	m.orgMu.Lock()
	defer m.orgMu.Unlock()

	m.delegated[memberAccountID] = struct{}{}

	return nil
}

// DeregisterOrganizationDelegatedAdmin removes a delegated administrator account.
func (m *Mock) DeregisterOrganizationDelegatedAdmin(_ context.Context, delegatedAdminAccountID string) error {
	m.orgMu.Lock()
	defer m.orgMu.Unlock()

	delete(m.delegated, delegatedAdminAccountID)

	return nil
}

// ListPublicKeys returns the digest-signing public keys for a time range. The
// emulator does not sign digest files, so this returns an empty list
// (documented).
func (*Mock) ListPublicKeys(
	_ context.Context, _, _ time.Time, _ string,
) ([]driver.PublicKey, string, error) {
	return []driver.PublicKey{}, "", nil
}

// ListInsightsData returns CloudTrail Insights events. The emulator generates no
// insight events, so this returns an empty list (documented).
func (*Mock) ListInsightsData(_ context.Context, _ string) ([]driver.InsightDataPoint, string, error) {
	return []driver.InsightDataPoint{}, "", nil
}

// ListInsightsMetricData returns Insights metric data points. Empty for the
// emulator (documented).
func (*Mock) ListInsightsMetricData(
	_ context.Context, _ string,
) ([]driver.InsightMetricPoint, string, error) {
	return []driver.InsightMetricPoint{}, "", nil
}

// SearchSampleQueries returns curated CloudTrail Lake sample queries matching a
// search phrase. The emulator returns a small fixed catalog (documented).
func (*Mock) SearchSampleQueries(
	_ context.Context, _, _ string, _ int32,
) ([]driver.SampleQuery, string, error) {
	return []driver.SampleQuery{
		{
			Name:        "Investigate console logins",
			Description: "Find all AWS Management Console sign-in events",
			SQL:         "SELECT eventTime, userIdentity.arn FROM $EDS WHERE eventName = 'ConsoleLogin'",
			Relevance:   1.0,
		},
	}, "", nil
}
