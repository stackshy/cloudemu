package route53resolver

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/route53resolver/driver"
)

// fwRuleKey builds the composite store key identifying a rule within a group.
func fwRuleKey(groupID, domainListID, qtype string) string {
	return groupID + "|" + domainListID + "|" + qtype
}

func cloneFWRule(r *driver.FirewallRule) driver.FirewallRule { return *r }

// ruleFromInput materializes a stored rule from an input, stamping timestamps.
func (m *Mock) ruleFromInput(in *driver.FirewallRuleInput) *driver.FirewallRule {
	return &driver.FirewallRule{
		FirewallRuleGroupID:             in.FirewallRuleGroupID,
		FirewallDomainListID:            in.FirewallDomainListID,
		Name:                            in.Name,
		Priority:                        in.Priority,
		Action:                          in.Action,
		BlockResponse:                   in.BlockResponse,
		BlockOverrideDomain:             in.BlockOverrideDomain,
		BlockOverrideDNSType:            in.BlockOverrideDNSType,
		BlockOverrideTTL:                in.BlockOverrideTTL,
		Qtype:                           in.Qtype,
		ConfidenceThreshold:             in.ConfidenceThreshold,
		DNSThreatProtection:             in.DNSThreatProtection,
		FirewallDomainRedirectionAction: in.FirewallDomainRedirectionAction,
		CreatorRequestID:                in.CreatorRequestID,
		Status:                          fwStatusComplete,
		CreatedAt:                       m.now(),
		ModifiedAt:                      m.now(),
	}
}

// refreshRuleCount recomputes a rule group's RuleCount. Caller holds m.mu.
func (m *Mock) refreshRuleCount(groupID string) {
	g, ok := m.fwRuleGroups.Get(groupID)
	if !ok {
		return
	}

	var n int

	for _, r := range m.fwRules.All() {
		if r.FirewallRuleGroupID == groupID {
			n++
		}
	}

	g.RuleCount = i32(n)
}

func (m *Mock) createRuleLocked(in *driver.FirewallRuleInput) (*driver.FirewallRule, error) {
	if !m.fwRuleGroups.Has(in.FirewallRuleGroupID) {
		return nil, fwRuleGroupNotFound(in.FirewallRuleGroupID)
	}

	r := m.ruleFromInput(in)
	m.fwRules.Set(fwRuleKey(in.FirewallRuleGroupID, in.FirewallDomainListID, in.Qtype), r)
	m.refreshRuleCount(in.FirewallRuleGroupID)

	return r, nil
}

func (m *Mock) updateRuleLocked(in *driver.FirewallRuleInput) (*driver.FirewallRule, error) {
	key := fwRuleKey(in.FirewallRuleGroupID, in.FirewallDomainListID, in.Qtype)

	r, ok := m.fwRules.Get(key)
	if !ok {
		return nil, errors.Newf(errors.NotFound,
			"firewall rule for group %q domain-list %q not found", in.FirewallRuleGroupID, in.FirewallDomainListID)
	}

	updated := m.ruleFromInput(in)
	updated.CreatedAt = r.CreatedAt
	m.fwRules.Set(key, updated)

	return updated, nil
}

func (m *Mock) deleteRuleLocked(groupID, domainListID, qtype string) (*driver.FirewallRule, error) {
	key := fwRuleKey(groupID, domainListID, qtype)

	r, ok := m.fwRules.Get(key)
	if !ok {
		return nil, errors.Newf(errors.NotFound,
			"firewall rule for group %q domain-list %q not found", groupID, domainListID)
	}

	m.fwRules.Delete(key)
	m.refreshRuleCount(groupID)

	out := cloneFWRule(r)
	out.Status = fwStatusDeleting

	return &out, nil
}

func (m *Mock) CreateFirewallRule(_ context.Context, in *driver.FirewallRuleInput) (*driver.FirewallRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, err := m.createRuleLocked(in)
	if err != nil {
		return nil, err
	}

	out := cloneFWRule(r)

	return &out, nil
}

func (m *Mock) UpdateFirewallRule(_ context.Context, in *driver.FirewallRuleInput) (*driver.FirewallRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, err := m.updateRuleLocked(in)
	if err != nil {
		return nil, err
	}

	out := cloneFWRule(r)

	return &out, nil
}

func (m *Mock) DeleteFirewallRule(
	_ context.Context, groupID, domainListID, qtype string,
) (*driver.FirewallRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.deleteRuleLocked(groupID, domainListID, qtype)
}

func (m *Mock) ListFirewallRules(_ context.Context, groupID string) ([]driver.FirewallRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]driver.FirewallRule, 0)

	for _, r := range m.fwRules.All() {
		if r.FirewallRuleGroupID == groupID {
			out = append(out, cloneFWRule(r))
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Priority < out[j].Priority
	})

	return out, nil
}

func (m *Mock) BatchCreateFirewallRules(
	_ context.Context, in []driver.FirewallRuleInput,
) ([]driver.FirewallRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]driver.FirewallRule, 0, len(in))

	for i := range in {
		r, err := m.createRuleLocked(&in[i])
		if err != nil {
			return nil, err
		}

		out = append(out, cloneFWRule(r))
	}

	return out, nil
}

func (m *Mock) BatchUpdateFirewallRules(
	_ context.Context, in []driver.FirewallRuleInput,
) ([]driver.FirewallRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]driver.FirewallRule, 0, len(in))

	for i := range in {
		r, err := m.updateRuleLocked(&in[i])
		if err != nil {
			return nil, err
		}

		out = append(out, cloneFWRule(r))
	}

	return out, nil
}

func (m *Mock) BatchDeleteFirewallRules(
	_ context.Context, groupID string, keys []driver.FirewallRuleKey,
) ([]driver.FirewallRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]driver.FirewallRule, 0, len(keys))

	for _, k := range keys {
		r, err := m.deleteRuleLocked(groupID, k.FirewallDomainListID, k.Qtype)
		if err != nil {
			return nil, err
		}

		out = append(out, *r)
	}

	return out, nil
}
