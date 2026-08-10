package configservice

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

// PutOrganizationConfigRule creates or updates an org-wide config rule (upsert).
//
//nolint:gocritic // rule is the driver OrganizationConfigRule input, taken by value to match the driver API.
func (m *Mock) PutOrganizationConfigRule(_ context.Context, rule driver.OrganizationConfigRule) (string, error) {
	if rule.Name == "" {
		return "", invalidParameter("OrganizationConfigRuleName is required")
	}

	if err := validateMaxExecutionFrequency(rule.MaximumExecutionFreq); err != nil {
		return "", err
	}

	now := m.now()
	rule.LastUpdateTime = now
	rule.ExcludedAccounts = copyStrings(rule.ExcludedAccounts)

	if existing, ok := m.orgRules.Get(rule.Name); ok {
		rule.Arn = existing.Arn
		m.orgRules.Set(rule.Name, &rule)

		return rule.Arn, nil
	}

	rule.Arn = m.arn("organization-config-rule/" + rule.Name)
	m.orgRules.Set(rule.Name, &rule)

	return rule.Arn, nil
}

func (m *Mock) allOrgRules() []driver.OrganizationConfigRule {
	keys := sortedKeys(m.orgRules.Keys())
	out := make([]driver.OrganizationConfigRule, 0, len(keys))

	for _, k := range keys {
		r, ok := m.orgRules.Get(k)
		if !ok {
			continue
		}

		cp := *r
		cp.ExcludedAccounts = copyStrings(r.ExcludedAccounts)
		cp.Tags = copyTags(r.Tags)
		out = append(out, cp)
	}

	return out
}

// DescribeOrganizationConfigRules returns the named org rules (all if empty).
func (m *Mock) DescribeOrganizationConfigRules(
	_ context.Context, names []string, page driver.Page,
) ([]driver.OrganizationConfigRule, string, error) {
	for _, n := range names {
		if !m.orgRules.Has(n) {
			return nil, "", noSuchOrgConfigRule(n)
		}
	}

	filtered := filterByNames(m.allOrgRules(), func(r driver.OrganizationConfigRule) string { return r.Name }, names)

	return paginate(filtered, page)
}

// DescribeOrganizationConfigRuleStatuses returns deployment statuses.
func (m *Mock) DescribeOrganizationConfigRuleStatuses(
	_ context.Context, names []string, page driver.Page,
) ([]driver.OrganizationConfigRule, string, error) {
	for _, n := range names {
		if !m.orgRules.Has(n) {
			return nil, "", noSuchOrgConfigRule(n)
		}
	}

	filtered := filterByNames(m.allOrgRules(), func(r driver.OrganizationConfigRule) string { return r.Name }, names)

	return paginate(filtered, page)
}

// DeleteOrganizationConfigRule removes an org rule.
func (m *Mock) DeleteOrganizationConfigRule(_ context.Context, name string) error {
	if !m.orgRules.Delete(name) {
		return noSuchOrgConfigRule(name)
	}

	return nil
}

// GetOrganizationConfigRuleDetailedStatus returns per-account status.
// Synthesized: empty (no member accounts in the emulator).
func (m *Mock) GetOrganizationConfigRuleDetailedStatus(
	_ context.Context, name string, page driver.Page,
) ([]driver.OrganizationConfigRule, string, error) {
	if !m.orgRules.Has(name) {
		return nil, "", noSuchOrgConfigRule(name)
	}

	return paginate([]driver.OrganizationConfigRule{}, page)
}

// GetOrganizationCustomRulePolicy returns the policy text for a custom-policy
// org rule. Synthesized empty.
func (m *Mock) GetOrganizationCustomRulePolicy(_ context.Context, name string) (string, error) {
	if !m.orgRules.Has(name) {
		return "", noSuchOrgConfigRule(name)
	}

	return "", nil
}

// PutOrganizationConformancePack creates or updates an org conformance pack.
//
//nolint:gocritic // pack taken by value to match the driver API.
func (m *Mock) PutOrganizationConformancePack(
	_ context.Context, pack driver.OrganizationConformancePack,
) (string, error) {
	if pack.Name == "" {
		return "", invalidParameter("OrganizationConformancePackName is required")
	}

	if (pack.TemplateBody == "") == (pack.TemplateS3URI == "") {
		return "", invalidParameter("exactly one of TemplateBody or TemplateS3Uri must be specified")
	}

	if err := validateTemplate(pack.TemplateBody, pack.TemplateS3URI); err != nil {
		return "", err
	}

	now := m.now()
	pack.LastUpdateTime = now
	pack.ExcludedAccounts = copyStrings(pack.ExcludedAccounts)
	pack.InputParameters = copyTags(pack.InputParameters)

	if existing, ok := m.orgPacks.Get(pack.Name); ok {
		pack.Arn = existing.Arn
		m.orgPacks.Set(pack.Name, &pack)

		return pack.Arn, nil
	}

	pack.Arn = m.arn("organization-conformance-pack/" + pack.Name)
	m.orgPacks.Set(pack.Name, &pack)

	return pack.Arn, nil
}

func (m *Mock) allOrgPacks() []driver.OrganizationConformancePack {
	keys := sortedKeys(m.orgPacks.Keys())
	out := make([]driver.OrganizationConformancePack, 0, len(keys))

	for _, k := range keys {
		p, ok := m.orgPacks.Get(k)
		if !ok {
			continue
		}

		cp := *p
		cp.ExcludedAccounts = copyStrings(p.ExcludedAccounts)
		cp.InputParameters = copyTags(p.InputParameters)
		cp.Tags = copyTags(p.Tags)
		out = append(out, cp)
	}

	return out
}

// DescribeOrganizationConformancePacks returns the named org packs.
func (m *Mock) DescribeOrganizationConformancePacks(
	_ context.Context, names []string, page driver.Page,
) ([]driver.OrganizationConformancePack, string, error) {
	for _, n := range names {
		if !m.orgPacks.Has(n) {
			return nil, "", noSuchOrgConformancePack(n)
		}
	}

	filtered := filterByNames(m.allOrgPacks(), func(p driver.OrganizationConformancePack) string { return p.Name }, names)

	return paginate(filtered, page)
}

// DescribeOrganizationConformancePackStatuses returns deployment statuses.
func (m *Mock) DescribeOrganizationConformancePackStatuses(
	_ context.Context, names []string, page driver.Page,
) ([]driver.OrganizationConformancePack, string, error) {
	for _, n := range names {
		if !m.orgPacks.Has(n) {
			return nil, "", noSuchOrgConformancePack(n)
		}
	}

	filtered := filterByNames(m.allOrgPacks(), func(p driver.OrganizationConformancePack) string { return p.Name }, names)

	return paginate(filtered, page)
}

// DeleteOrganizationConformancePack removes an org pack.
func (m *Mock) DeleteOrganizationConformancePack(_ context.Context, name string) error {
	if !m.orgPacks.Delete(name) {
		return noSuchOrgConformancePack(name)
	}

	return nil
}

// GetOrganizationConformancePackDetailedStatus returns per-account status.
// Synthesized empty.
func (m *Mock) GetOrganizationConformancePackDetailedStatus(
	_ context.Context, name string, page driver.Page,
) ([]driver.OrganizationConformancePack, string, error) {
	if !m.orgPacks.Has(name) {
		return nil, "", noSuchOrgConformancePack(name)
	}

	return paginate([]driver.OrganizationConformancePack{}, page)
}
