// Package networkfirewall provides an in-memory mock of AWS Network Firewall.
package networkfirewall

import (
	"context"
	"fmt"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

var _ nfdriver.NetworkFirewall = (*Mock)(nil)

// Mock is the in-memory AWS Network Firewall implementation. Resources are
// keyed by name (unique per account/region, as in the real service).
type Mock struct {
	firewalls  *memstore.Store[*nfdriver.Firewall]
	policies   *memstore.Store[*nfdriver.FirewallPolicy]
	ruleGroups *memstore.Store[*nfdriver.RuleGroup]
	opts       *config.Options
}

// New creates a new Network Firewall mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		firewalls:  memstore.New[*nfdriver.Firewall](),
		policies:   memstore.New[*nfdriver.FirewallPolicy](),
		ruleGroups: memstore.New[*nfdriver.RuleGroup](),
		opts:       opts,
	}
}

func (m *Mock) arn(kind, name string) string {
	return idgen.AWSARN("network-firewall", m.opts.Region, m.opts.AccountID, kind+"/"+name)
}

func copyTags(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}

	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}

	return out
}

func cloneStrings(s []string) []string {
	if len(s) == 0 {
		return nil
	}

	return append([]string(nil), s...)
}

// ---- Firewalls ----

//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) CreateFirewall(_ context.Context, cfg nfdriver.CreateFirewallConfig) (*nfdriver.Firewall, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "FirewallName is required")
	}

	if m.firewalls.Has(cfg.Name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "firewall %q already exists", cfg.Name)
	}

	fw := &nfdriver.Firewall{
		Name:             cfg.Name,
		ARN:              m.arn("firewall", cfg.Name),
		PolicyARN:        cfg.PolicyARN,
		VPCID:            cfg.VPCID,
		SubnetIDs:        cloneStrings(cfg.SubnetIDs),
		Description:      cfg.Description,
		DeleteProtection: cfg.DeleteProtection,
		Status:           "READY",
		Tags:             copyTags(cfg.Tags),
	}
	m.firewalls.Set(cfg.Name, fw)

	out := cloneFirewall(fw)

	return &out, nil
}

func (m *Mock) DescribeFirewall(_ context.Context, name, arn string) (*nfdriver.Firewall, error) {
	fw, ok := m.lookupFirewall(name, arn)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "firewall %q not found", nonEmpty(name, arn))
	}

	out := cloneFirewall(fw)

	return &out, nil
}

func (m *Mock) DeleteFirewall(_ context.Context, name, arn string) (*nfdriver.Firewall, error) {
	fw, ok := m.lookupFirewall(name, arn)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "firewall %q not found", nonEmpty(name, arn))
	}

	if fw.DeleteProtection {
		return nil, cerrors.Newf(cerrors.FailedPrecondition, "firewall %q has delete protection enabled", fw.Name)
	}

	fw.Status = "DELETING"
	m.firewalls.Delete(fw.Name)

	out := cloneFirewall(fw)

	return &out, nil
}

func (m *Mock) ListFirewalls(_ context.Context) ([]nfdriver.Firewall, error) {
	all := m.firewalls.SortedValues()
	out := make([]nfdriver.Firewall, 0, len(all))

	for _, f := range all {
		out = append(out, cloneFirewall(f))
	}

	return out, nil
}

func (m *Mock) lookupFirewall(name, arn string) (*nfdriver.Firewall, bool) {
	if name != "" {
		return m.firewalls.Get(name)
	}

	for _, f := range m.firewalls.SortedValues() {
		if f.ARN == arn {
			return f, true
		}
	}

	return nil, false
}

func cloneFirewall(f *nfdriver.Firewall) nfdriver.Firewall {
	out := *f
	out.SubnetIDs = cloneStrings(f.SubnetIDs)
	out.Tags = copyTags(f.Tags)

	return out
}

// ---- Firewall Policies ----

//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) CreateFirewallPolicy(_ context.Context, cfg nfdriver.CreateFirewallPolicyConfig) (*nfdriver.FirewallPolicy, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "FirewallPolicyName is required")
	}

	if m.policies.Has(cfg.Name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "firewall policy %q already exists", cfg.Name)
	}

	p := &nfdriver.FirewallPolicy{
		Name:                            cfg.Name,
		ARN:                             m.arn("firewall-policy", cfg.Name),
		ID:                              idgen.GenerateID(""),
		Description:                     cfg.Description,
		StatelessDefaultActions:         cloneStrings(cfg.StatelessDefaultActions),
		StatelessFragmentDefaultActions: cloneStrings(cfg.StatelessFragmentDefaultActions),
		Tags:                            copyTags(cfg.Tags),
	}
	m.policies.Set(cfg.Name, p)

	out := cloneFirewallPolicy(p)

	return &out, nil
}

func (m *Mock) DescribeFirewallPolicy(_ context.Context, name, arn string) (*nfdriver.FirewallPolicy, error) {
	p, ok := m.lookupPolicy(name, arn)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "firewall policy %q not found", nonEmpty(name, arn))
	}

	out := cloneFirewallPolicy(p)

	return &out, nil
}

func (m *Mock) DeleteFirewallPolicy(_ context.Context, name, arn string) (*nfdriver.FirewallPolicy, error) {
	p, ok := m.lookupPolicy(name, arn)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "firewall policy %q not found", nonEmpty(name, arn))
	}

	m.policies.Delete(p.Name)

	out := cloneFirewallPolicy(p)

	return &out, nil
}

func (m *Mock) ListFirewallPolicies(_ context.Context) ([]nfdriver.FirewallPolicy, error) {
	all := m.policies.SortedValues()
	out := make([]nfdriver.FirewallPolicy, 0, len(all))

	for _, p := range all {
		out = append(out, cloneFirewallPolicy(p))
	}

	return out, nil
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

func cloneFirewallPolicy(p *nfdriver.FirewallPolicy) nfdriver.FirewallPolicy {
	out := *p
	out.StatelessDefaultActions = cloneStrings(p.StatelessDefaultActions)
	out.StatelessFragmentDefaultActions = cloneStrings(p.StatelessFragmentDefaultActions)
	out.Tags = copyTags(p.Tags)

	return out
}

// ---- Rule Groups ----

//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) CreateRuleGroup(_ context.Context, cfg nfdriver.CreateRuleGroupConfig) (*nfdriver.RuleGroup, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "RuleGroupName is required")
	}

	if cfg.Type != "STATEFUL" && cfg.Type != "STATELESS" {
		return nil, cerrors.New(cerrors.InvalidArgument, "Type must be STATEFUL or STATELESS")
	}

	key := ruleGroupKey(cfg.Name, cfg.Type)
	if m.ruleGroups.Has(key) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "rule group %q already exists", cfg.Name)
	}

	rg := &nfdriver.RuleGroup{
		Name:        cfg.Name,
		ARN:         m.arn("stateful-rulegroup", cfg.Name),
		ID:          idgen.GenerateID(""),
		Type:        cfg.Type,
		Capacity:    cfg.Capacity,
		Description: cfg.Description,
		Tags:        copyTags(cfg.Tags),
	}
	m.ruleGroups.Set(key, rg)

	out := cloneRuleGroup(rg)

	return &out, nil
}

func (m *Mock) DescribeRuleGroup(_ context.Context, name, arn, ruleType string) (*nfdriver.RuleGroup, error) {
	rg, ok := m.lookupRuleGroup(name, arn, ruleType)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "rule group %q not found", nonEmpty(name, arn))
	}

	out := cloneRuleGroup(rg)

	return &out, nil
}

func (m *Mock) DeleteRuleGroup(_ context.Context, name, arn, ruleType string) (*nfdriver.RuleGroup, error) {
	rg, ok := m.lookupRuleGroup(name, arn, ruleType)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "rule group %q not found", nonEmpty(name, arn))
	}

	m.ruleGroups.Delete(ruleGroupKey(rg.Name, rg.Type))

	out := cloneRuleGroup(rg)

	return &out, nil
}

func (m *Mock) ListRuleGroups(_ context.Context) ([]nfdriver.RuleGroup, error) {
	all := m.ruleGroups.SortedValues()
	out := make([]nfdriver.RuleGroup, 0, len(all))

	for _, rg := range all {
		out = append(out, cloneRuleGroup(rg))
	}

	return out, nil
}

func (m *Mock) lookupRuleGroup(name, arn, ruleType string) (*nfdriver.RuleGroup, bool) {
	if name != "" && ruleType != "" {
		return m.ruleGroups.Get(ruleGroupKey(name, ruleType))
	}

	for _, rg := range m.ruleGroups.SortedValues() {
		if (name != "" && rg.Name == name) || (arn != "" && rg.ARN == arn) {
			return rg, true
		}
	}

	return nil, false
}

func ruleGroupKey(name, ruleType string) string {
	return fmt.Sprintf("%s/%s", ruleType, name)
}

func cloneRuleGroup(rg *nfdriver.RuleGroup) nfdriver.RuleGroup {
	out := *rg
	out.Tags = copyTags(rg.Tags)

	return out
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}

	return b
}
