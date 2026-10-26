package networkfirewall

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) CreateFirewallPolicy(_ context.Context, cfg nfdriver.CreateFirewallPolicyConfig) (*nfdriver.FirewallPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := validateName("FirewallPolicyName", cfg.Name); err != nil {
		return nil, err
	}

	if err := validateTags(cfg.Tags); err != nil {
		return nil, err
	}

	spec := policySpec{
		description: cfg.Description, statelessDefault: cfg.StatelessDefaultActions,
		statelessFragmentDefault: cfg.StatelessFragmentDefaultActions, statefulDefault: cfg.StatefulDefaultActions,
		statefulRefs: cfg.StatefulRuleGroupReferences, statelessRefs: cfg.StatelessRuleGroupReferences,
		tlsARN: cfg.TLSInspectionConfigurationARN, extra: cfg.Extra,
	}
	if err := m.validatePolicy(&spec, nil); err != nil {
		return nil, err
	}

	p := &nfdriver.FirewallPolicy{
		Name:                            cfg.Name,
		ARN:                             m.arn("firewall-policy", cfg.Name),
		ID:                              idgen.GenerateID(""),
		Description:                     cfg.Description,
		StatelessDefaultActions:         cloneStrings(cfg.StatelessDefaultActions),
		StatelessFragmentDefaultActions: cloneStrings(cfg.StatelessFragmentDefaultActions),
		StatefulDefaultActions:          cloneStrings(cfg.StatefulDefaultActions),
		StatefulRuleGroupReferences:     cloneRuleGroupRefs(cfg.StatefulRuleGroupReferences),
		StatelessRuleGroupReferences:    cloneRuleGroupRefs(cfg.StatelessRuleGroupReferences),
		TLSInspectionConfigurationARN:   cfg.TLSInspectionConfigurationARN,
		Extra:                           clonePolicyExtra(&cfg.Extra),
		Status:                          statusActive,
		LastModified:                    m.now(),
		UpdateToken:                     newToken(),
		Tags:                            copyTags(cfg.Tags),
	}
	if !m.policies.SetIfAbsent(cfg.Name, p) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "firewall policy %q already exists", cfg.Name)
	}

	return m.outPolicy(p), nil
}

func (m *Mock) DescribeFirewallPolicy(_ context.Context, name, arn string) (*nfdriver.FirewallPolicy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := requireIdentifier("FirewallPolicy", name, arn); err != nil {
		return nil, err
	}

	p, ok := m.lookupPolicy(name, arn)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "firewall policy %q not found", nonEmpty(name, arn))
	}

	return m.outPolicy(p), nil
}

//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) UpdateFirewallPolicy(
	_ context.Context, name, arn string, cfg nfdriver.UpdateFirewallPolicyConfig,
) (*nfdriver.FirewallPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := requireIdentifier("FirewallPolicy", name, arn); err != nil {
		return nil, err
	}

	if err := requireToken(cfg.UpdateToken); err != nil {
		return nil, err
	}

	p, ok := m.lookupPolicy(name, arn)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "firewall policy %q not found", nonEmpty(name, arn))
	}

	if err := checkToken(p.UpdateToken, cfg.UpdateToken); err != nil {
		return nil, err
	}

	spec := policySpec{
		description: cfg.Description, statelessDefault: cfg.StatelessDefaultActions,
		statelessFragmentDefault: cfg.StatelessFragmentDefaultActions, statefulDefault: cfg.StatefulDefaultActions,
		statefulRefs: cfg.StatefulRuleGroupReferences, statelessRefs: cfg.StatelessRuleGroupReferences,
		tlsARN: cfg.TLSInspectionConfigurationARN, extra: cfg.Extra,
	}
	if err := m.validatePolicy(&spec, p); err != nil {
		return nil, err
	}

	p.Description = cfg.Description
	p.StatelessDefaultActions = cloneStrings(cfg.StatelessDefaultActions)
	p.StatelessFragmentDefaultActions = cloneStrings(cfg.StatelessFragmentDefaultActions)
	p.StatefulDefaultActions = cloneStrings(cfg.StatefulDefaultActions)
	p.StatefulRuleGroupReferences = cloneRuleGroupRefs(cfg.StatefulRuleGroupReferences)
	p.StatelessRuleGroupReferences = cloneRuleGroupRefs(cfg.StatelessRuleGroupReferences)
	p.TLSInspectionConfigurationARN = cfg.TLSInspectionConfigurationARN
	p.Extra = clonePolicyExtra(&cfg.Extra)
	p.LastModified = m.now()
	p.UpdateToken = newToken()

	return m.outPolicy(p), nil
}

func (m *Mock) DeleteFirewallPolicy(_ context.Context, name, arn string) (*nfdriver.FirewallPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := requireIdentifier("FirewallPolicy", name, arn); err != nil {
		return nil, err
	}

	p, ok := m.lookupPolicy(name, arn)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "firewall policy %q not found", nonEmpty(name, arn))
	}

	if m.policyInUse(p.ARN) {
		return nil, nfdriver.InvalidOperation("firewall policy %q is in use by a firewall", p.Name)
	}

	out := m.outPolicy(p)
	out.Status = statusDeleting

	m.policies.Delete(p.Name)
	delete(m.resourcePolicies, p.ARN)

	return out, nil
}

func (m *Mock) ListFirewallPolicies(_ context.Context, opts nfdriver.ListOptions) ([]nfdriver.FirewallPolicy, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	items, next, err := paginate(m.policies.SortedValues(), opts)
	if err != nil {
		return nil, "", err
	}

	out := make([]nfdriver.FirewallPolicy, 0, len(items))
	for _, p := range items {
		out = append(out, *m.outPolicy(p))
	}

	return out, next, nil
}

func (m *Mock) lookupPolicy(name, arn string) (*nfdriver.FirewallPolicy, bool) {
	if name != "" {
		return m.policies.Get(name)
	}

	for _, p := range m.policies.SortedValues() {
		if p.ARN == arn {
			return p, true
		}
	}

	return nil, false
}

// policyExists reports whether a firewall policy with the given ARN is present.
func (m *Mock) policyExists(arn string) bool {
	for _, p := range m.policies.SortedValues() {
		if p.ARN == arn {
			return true
		}
	}

	return false
}

// policyInUse reports whether any firewall references the policy ARN.
func (m *Mock) policyInUse(arn string) bool {
	return m.policyAssociationCount(arn) > 0
}

func (m *Mock) policyAssociationCount(arn string) int {
	n := 0

	for _, f := range m.firewalls.SortedValues() {
		if f.PolicyARN == arn {
			n++
		}
	}

	return n
}

// outPolicy returns a deep copy of the stored policy with the computed
// capacity, association count and status filled in.
func (m *Mock) outPolicy(p *nfdriver.FirewallPolicy) *nfdriver.FirewallPolicy {
	out := *p
	out.StatelessDefaultActions = cloneStrings(p.StatelessDefaultActions)
	out.StatelessFragmentDefaultActions = cloneStrings(p.StatelessFragmentDefaultActions)
	out.StatefulDefaultActions = cloneStrings(p.StatefulDefaultActions)
	out.StatefulRuleGroupReferences = cloneRuleGroupRefs(p.StatefulRuleGroupReferences)
	out.StatelessRuleGroupReferences = cloneRuleGroupRefs(p.StatelessRuleGroupReferences)
	out.Extra = clonePolicyExtra(&p.Extra)
	out.Tags = copyTags(p.Tags)
	out.NumberOfAssociations = m.policyAssociationCount(p.ARN)
	out.ConsumedStatelessRuleCapacity = m.referencedCapacity(p.StatelessRuleGroupReferences)

	stateful, domain := 0, 0

	for _, r := range p.StatefulRuleGroupReferences {
		rg, ok := m.ruleGroupByARN(r.ResourceARN)
		if !ok {
			continue
		}

		if rg.Type == ruleTypeStatefulDomain {
			domain += rg.Capacity
		} else {
			stateful += rg.Capacity
		}
	}

	out.ConsumedStatefulRuleCapacity = stateful
	out.ConsumedStatefulDomainCapacity = domain

	return &out
}

// referencedCapacity sums the capacity AWS reserves for referenced rule groups.
func (m *Mock) referencedCapacity(refs []nfdriver.RuleGroupReference) int {
	total := 0

	for _, r := range refs {
		if rg, ok := m.ruleGroupByARN(r.ResourceARN); ok {
			total += rg.Capacity
		}
	}

	return total
}

func cloneRuleGroupRefs(refs []nfdriver.RuleGroupReference) []nfdriver.RuleGroupReference {
	if len(refs) == 0 {
		return nil
	}

	out := make([]nfdriver.RuleGroupReference, len(refs))
	for i, r := range refs {
		out[i] = nfdriver.RuleGroupReference{
			ResourceARN: r.ResourceARN, Priority: r.Priority,
			Override: cloneRawJSON(r.Override), DeepThreatInspection: cloneBool(r.DeepThreatInspection),
		}
	}

	return out
}

func clonePolicyExtra(e *nfdriver.PolicyExtra) nfdriver.PolicyExtra {
	return nfdriver.PolicyExtra{
		StatelessCustomActions:  cloneRawJSON(e.StatelessCustomActions),
		PolicyVariables:         cloneRawJSON(e.PolicyVariables),
		StatefulEngineOptions:   cloneRawJSON(e.StatefulEngineOptions),
		EnableTLSSessionHolding: cloneBool(e.EnableTLSSessionHolding),
	}
}
