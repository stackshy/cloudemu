package vpc

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// CreateIpamPolicy creates an allocation policy for an IPAM.
func (m *Mock) CreateIpamPolicy(_ context.Context, ipamID string, tags map[string]string) (*driver.IpamPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ipam, ok := m.ipams.Get(ipamID)
	if !ok {
		return nil, errors.Newf(errors.InvalidArgument, "ipam %q not found", ipamID)
	}

	id := idgen.GenerateID("ipam-policy-")
	p := &driver.IpamPolicy{
		ID: id, ARN: m.ipamARN("ipam-policy/" + id), IpamID: ipamID, IpamRegion: ipam.Region,
		OwnerID: m.opts.AccountID, State: "create-complete", Tags: copyTags(tags),
	}
	m.ipamPolicies.Set(id, p)

	out := cloneIpamPolicy(p)

	return &out, nil
}

// DeleteIpamPolicy deletes an IPAM policy.
func (m *Mock) DeleteIpamPolicy(_ context.Context, id string) (*driver.IpamPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.ipamPolicies.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "ipam policy %q not found", id)
	}

	p.State = ipamStateDeleteComplete

	m.ipamPolicies.Delete(id)

	out := cloneIpamPolicy(p)

	return &out, nil
}

// DescribeIpamPolicies returns policies matching ids.
func (m *Mock) DescribeIpamPolicies(_ context.Context, ids []string) ([]driver.IpamPolicy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.ipamPolicies, ids, cloneIpamPolicy), nil
}

// EnableIpamPolicy enables a policy (optionally for an org target).
func (m *Mock) EnableIpamPolicy(_ context.Context, id, _ string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.ipamPolicies.Get(id)
	if !ok {
		return "", errors.Newf(errors.NotFound, "ipam policy %q not found", id)
	}

	p.Enabled = true

	return p.ID, nil
}

// DisableIpamPolicy disables a policy.
func (m *Mock) DisableIpamPolicy(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.ipamPolicies.Get(id)
	if !ok {
		return errors.Newf(errors.NotFound, "ipam policy %q not found", id)
	}

	p.Enabled = false

	return nil
}

// GetEnabledIpamPolicy returns the currently-enabled policy, if any.
func (m *Mock) GetEnabledIpamPolicy(_ context.Context) (policyID string, enabled bool, managedBy string, err error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, p := range m.ipamPolicies.SortedValues() {
		if p.Enabled {
			return p.ID, true, m.opts.AccountID, nil
		}
	}

	return "", false, "", nil
}

// ModifyIpamPolicyAllocationRules replaces a policy's allocation rules.
func (m *Mock) ModifyIpamPolicyAllocationRules(_ context.Context, id string, rules []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.ipamPolicies.Get(id)
	if !ok {
		return errors.Newf(errors.NotFound, "ipam policy %q not found", id)
	}

	p.AllocationRules = append([]string(nil), rules...)

	return nil
}

// GetIpamPolicyAllocationRules returns a policy's allocation rule documents.
func (m *Mock) GetIpamPolicyAllocationRules(_ context.Context, id string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.ipamPolicies.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "ipam policy %q not found", id)
	}

	return append([]string(nil), p.AllocationRules...), nil
}

// GetIpamPolicyOrganizationTargets returns the org targets a policy applies to.
// The emulator is single-account, so it reports the configured account.
func (m *Mock) GetIpamPolicyOrganizationTargets(_ context.Context, id string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.ipamPolicies.Has(id) {
		return nil, errors.Newf(errors.NotFound, "ipam policy %q not found", id)
	}

	return []string{m.opts.AccountID}, nil
}

// EnableIpamOrganizationAdminAccount delegates IPAM admin to an account.
func (*Mock) EnableIpamOrganizationAdminAccount(_ context.Context, accountID string) (bool, error) {
	if accountID == "" {
		return false, errors.New(errors.InvalidArgument, "delegatedAdminAccountId is required")
	}

	return true, nil
}

// DisableIpamOrganizationAdminAccount removes the delegated IPAM admin.
func (*Mock) DisableIpamOrganizationAdminAccount(_ context.Context, accountID string) (bool, error) {
	if accountID == "" {
		return false, errors.New(errors.InvalidArgument, "delegatedAdminAccountId is required")
	}

	return true, nil
}

func cloneIpamPolicy(p *driver.IpamPolicy) driver.IpamPolicy {
	out := *p
	out.AllocationRules = append([]string(nil), p.AllocationRules...)
	out.Tags = copyTags(p.Tags)

	return out
}
