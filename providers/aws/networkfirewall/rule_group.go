package networkfirewall

import (
	"context"
	"fmt"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

// summaryRuleOptions are the SDK SummaryRuleOption values.
//
//nolint:gochecknoglobals // immutable enum value sets.
var (
	summaryRuleOptions = []string{"SID", "MSG", "METADATA"}
	managedTypes       = []string{"AWS_MANAGED_THREAT_SIGNATURES", "AWS_MANAGED_DOMAIN_LISTS", "ACTIVE_THREAT_DEFENSE", "PARTNER_MANAGED"}
)

//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) CreateRuleGroup(_ context.Context, cfg nfdriver.CreateRuleGroupConfig) (*nfdriver.RuleGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := validateName("RuleGroupName", cfg.Name); err != nil {
		return nil, err
	}

	if err := validateRuleGroupBody(cfg.Description, cfg.Tags, cfg.SummaryRuleOptions); err != nil {
		return nil, err
	}

	if !oneOf(cfg.Type, ruleTypeStateful, ruleTypeStateless, ruleTypeStatefulDomain) {
		return nil, invalid("Type must be one of STATELESS, STATEFUL, STATEFUL_DOMAIN")
	}

	if cfg.Capacity < 1 {
		return nil, invalid("Capacity must be at least 1, got %d", cfg.Capacity)
	}

	consumed, err := consumedCapacity(cfg.Rules)
	if err != nil {
		return nil, err
	}

	if consumed > cfg.Capacity {
		return nil, invalid("the rules need %d capacity units but Capacity is %d", consumed, cfg.Capacity)
	}

	rgKind := "stateful-rulegroup"
	if cfg.Type == ruleTypeStateless {
		rgKind = "stateless-rulegroup"
	}

	rg := &nfdriver.RuleGroup{
		Name:               cfg.Name,
		ARN:                m.arn(rgKind, cfg.Name),
		ID:                 idgen.GenerateID(""),
		Type:               cfg.Type,
		Capacity:           cfg.Capacity,
		Description:        cfg.Description,
		Rules:              cloneRawJSON(cfg.Rules),
		SummaryRuleOptions: cloneStrings(cfg.SummaryRuleOptions),
		Status:             statusActive,
		LastModified:       m.now(),
		UpdateToken:        newToken(),
		Tags:               copyTags(cfg.Tags),
	}
	// STATEFUL and STATEFUL_DOMAIN groups share the stateful ARN namespace.
	if _, taken := m.ruleGroupByARN(rg.ARN); taken {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "rule group %q already exists", cfg.Name)
	}

	if !m.ruleGroups.SetIfAbsent(ruleGroupKey(cfg.Name, cfg.Type), rg) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "rule group %q already exists", cfg.Name)
	}

	return m.outRuleGroup(rg), nil
}

func validateRuleGroupBody(description string, tags map[string]string, summaryOpts []string) error {
	if err := validateDescription(description); err != nil {
		return err
	}

	if err := validateTags(tags); err != nil {
		return err
	}

	for _, o := range summaryOpts {
		if !oneOf(o, summaryRuleOptions...) {
			return invalid("SummaryConfiguration.RuleOptions value %q is not valid", o)
		}
	}

	return nil
}

func (m *Mock) DescribeRuleGroup(_ context.Context, name, arn, ruleType string) (*nfdriver.RuleGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rg, err := m.findRuleGroup(name, arn, ruleType)
	if err != nil {
		return nil, err
	}

	return m.outRuleGroup(rg), nil
}

//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) UpdateRuleGroup(
	_ context.Context, name, arn, ruleType string, cfg nfdriver.UpdateRuleGroupConfig,
) (*nfdriver.RuleGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := requireToken(cfg.UpdateToken); err != nil {
		return nil, err
	}

	rg, err := m.findRuleGroup(name, arn, ruleType)
	if err != nil {
		return nil, err
	}

	if err = checkToken(rg.UpdateToken, cfg.UpdateToken); err != nil {
		return nil, err
	}

	if err = validateRuleGroupBody(cfg.Description, nil, cfg.SummaryRuleOptions); err != nil {
		return nil, err
	}

	rules := rg.Rules
	if len(cfg.Rules) > 0 {
		rules = cfg.Rules
	}

	consumed, err := consumedCapacity(rules)
	if err != nil {
		return nil, err
	}

	if consumed > rg.Capacity {
		return nil, invalid("the rules need %d capacity units but the rule group capacity is %d", consumed, rg.Capacity)
	}

	rg.Description = cfg.Description
	rg.Rules = cloneRawJSON(rules)

	if cfg.SummaryRuleOptions != nil {
		rg.SummaryRuleOptions = cloneStrings(cfg.SummaryRuleOptions)
	}

	rg.LastModified = m.now()
	rg.UpdateToken = newToken()

	return m.outRuleGroup(rg), nil
}

func (m *Mock) DeleteRuleGroup(_ context.Context, name, arn, ruleType string) (*nfdriver.RuleGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rg, err := m.findRuleGroup(name, arn, ruleType)
	if err != nil {
		return nil, err
	}

	if n := m.ruleGroupRefCount(rg.ARN); n > 0 {
		return nil, nfdriver.InvalidOperation("rule group %q is referenced by %d firewall policy(ies)", rg.Name, n)
	}

	out := m.outRuleGroup(rg)
	out.Status = statusDeleting

	m.ruleGroups.Delete(ruleGroupKey(rg.Name, rg.Type))
	delete(m.resourcePolicies, rg.ARN)

	return out, nil
}

func (m *Mock) ListRuleGroups(
	_ context.Context, filter nfdriver.RuleGroupFilter, opts nfdriver.ListOptions,
) ([]nfdriver.RuleGroup, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := validateRuleGroupFilter(filter); err != nil {
		return nil, "", err
	}

	items, next, err := paginate(m.matchRuleGroups(filter), opts)
	if err != nil {
		return nil, "", err
	}

	out := make([]nfdriver.RuleGroup, 0, len(items))
	for _, rg := range items {
		out = append(out, *m.outRuleGroup(rg))
	}

	return out, next, nil
}

func validateRuleGroupFilter(f nfdriver.RuleGroupFilter) error {
	if f.Type != "" && !oneOf(f.Type, ruleTypeStateful, ruleTypeStateless, ruleTypeStatefulDomain) {
		return invalid("Type %q is not valid", f.Type)
	}

	if f.Scope != "" && !oneOf(f.Scope, "MANAGED", "ACCOUNT") {
		return invalid("Scope %q is not valid", f.Scope)
	}

	if f.ManagedType != "" && !oneOf(f.ManagedType, managedTypes...) {
		return invalid("ManagedType %q is not valid", f.ManagedType)
	}

	return nil
}

// matchRuleGroups returns the rule groups a filter selects, in name order.
// Every rule group the emulator holds is account-owned, so a MANAGED scope or a
// ManagedType selects none.
func (m *Mock) matchRuleGroups(f nfdriver.RuleGroupFilter) []*nfdriver.RuleGroup {
	if f.Scope == "MANAGED" || f.ManagedType != "" {
		return nil
	}

	var matched []*nfdriver.RuleGroup

	for _, rg := range m.ruleGroups.SortedValues() {
		if f.Type == "" || rg.Type == f.Type {
			matched = append(matched, rg)
		}
	}

	return matched
}

// findRuleGroup resolves a rule group by name+type or by ARN, validating the
// identifier first.
func (m *Mock) findRuleGroup(name, arn, ruleType string) (*nfdriver.RuleGroup, error) {
	if err := requireIdentifier("RuleGroup", name, arn); err != nil {
		return nil, err
	}

	if name != "" && ruleType == "" && arn == "" {
		return nil, invalid("Type is required when RuleGroupArn is not set")
	}

	if name != "" && ruleType != "" && !oneOf(ruleType, ruleTypeStateful, ruleTypeStateless, ruleTypeStatefulDomain) {
		return nil, invalid("Type %q is not valid", ruleType)
	}

	rg, ok := m.lookupRuleGroup(name, arn, ruleType)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "rule group %q not found", nonEmpty(name, arn))
	}

	return rg, nil
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

func (m *Mock) ruleGroupByARN(arn string) (*nfdriver.RuleGroup, bool) {
	for _, rg := range m.ruleGroups.SortedValues() {
		if rg.ARN == arn {
			return rg, true
		}
	}

	return nil, false
}

// ruleGroupRefCount counts the firewall policies that reference the rule group.
func (m *Mock) ruleGroupRefCount(arn string) int {
	n := 0

	for _, p := range m.policies.SortedValues() {
		if refsContain(p.StatefulRuleGroupReferences, arn) || refsContain(p.StatelessRuleGroupReferences, arn) {
			n++
		}
	}

	return n
}

func refsContain(refs []nfdriver.RuleGroupReference, arn string) bool {
	for _, r := range refs {
		if r.ResourceARN == arn {
			return true
		}
	}

	return false
}

func ruleGroupKey(name, ruleType string) string {
	return fmt.Sprintf("%s/%s", ruleType, name)
}

// outRuleGroup returns a deep copy of the stored rule group with the computed
// consumed capacity and association count filled in.
func (m *Mock) outRuleGroup(rg *nfdriver.RuleGroup) *nfdriver.RuleGroup {
	out := *rg
	out.Rules = cloneRawJSON(rg.Rules)
	out.SummaryRuleOptions = cloneStrings(rg.SummaryRuleOptions)
	out.Tags = copyTags(rg.Tags)
	out.NumberOfAssociations = m.ruleGroupRefCount(rg.ARN)
	out.ConsumedCapacity, _ = consumedCapacity(rg.Rules)

	return &out
}
