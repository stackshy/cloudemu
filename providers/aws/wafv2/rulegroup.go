package wafv2

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/wafv2/driver"
)

func copyRuleGroup(g *driver.RuleGroup) driver.RuleGroup {
	out := *g
	out.Tags = copyTags(g.Tags)
	out.VisibilityConfig = copyBytes(g.VisibilityConfig)
	out.Rules = copyBytes(g.Rules)
	out.CustomResponses = copyBytes(g.CustomResponses)

	return out
}

func (m *Mock) ruleGroupByName(scope, name string) bool {
	for _, gd := range m.ruleGrps.All() {
		gd.mu.RLock()
		match := gd.grp.Scope == scope && gd.grp.Name == name
		gd.mu.RUnlock()

		if match {
			return true
		}
	}

	return false
}

// CreateRuleGroup creates a rule group, storing its rules verbatim.
//
//nolint:gocritic // in is the public input, taken by value to match the driver API.
func (m *Mock) CreateRuleGroup(_ context.Context, in driver.CreateRuleGroupInput) (*driver.RuleGroup, error) {
	if in.Name == "" || in.Scope == "" {
		return nil, invalidParameter("Name and Scope are required")
	}

	if err := validateScope(in.Scope); err != nil {
		return nil, err
	}

	m.createMu.Lock()
	defer m.createMu.Unlock()

	if m.ruleGroupByName(in.Scope, in.Name) {
		return nil, duplicate("rule group %q already exists in scope %s", in.Name, in.Scope)
	}

	id := idgen.GenerateID("")
	grp := driver.RuleGroup{
		ID:               id,
		Name:             in.Name,
		ARN:              m.arn(in.Scope, "rulegroup", in.Name, id),
		Scope:            in.Scope,
		Description:      in.Description,
		LockToken:        newLockToken(),
		Capacity:         in.Capacity,
		LabelNamespace:   "awswaf:" + m.opts.AccountID + ":rulegroup:" + in.Name + ":",
		VisibilityConfig: in.VisibilityConfig,
		Rules:            in.Rules,
		CustomResponses:  in.CustomResponses,
		Tags:             copyTags(in.Tags),
	}

	m.ruleGrps.Set(key(in.Scope, id), &ruleGroupData{grp: grp})

	out := copyRuleGroup(&grp)

	return &out, nil
}

func (m *Mock) getRuleGroupData(ref driver.Ref) (*ruleGroupData, error) {
	gd, ok := m.ruleGrps.Get(key(ref.Scope, ref.ID))
	if !ok {
		return nil, nonexistent("rule group %q not found in scope %s", ref.ID, ref.Scope)
	}

	return gd, nil
}

// GetRuleGroup returns a rule group by (scope,id).
func (m *Mock) GetRuleGroup(_ context.Context, ref driver.Ref) (*driver.RuleGroup, error) {
	gd, err := m.getRuleGroupData(ref)
	if err != nil {
		return nil, err
	}

	gd.mu.RLock()
	defer gd.mu.RUnlock()

	out := copyRuleGroup(&gd.grp)

	return &out, nil
}

// UpdateRuleGroup replaces a rule group's mutable fields, enforcing the lock token.
//
//nolint:gocritic // in is the public input, taken by value to match the driver API.
func (m *Mock) UpdateRuleGroup(_ context.Context, in driver.UpdateRuleGroupInput) (string, error) {
	gd, err := m.getRuleGroupData(driver.Ref{Scope: in.Scope, ID: in.ID})
	if err != nil {
		return "", err
	}

	gd.mu.Lock()
	defer gd.mu.Unlock()

	if gd.grp.LockToken != in.LockToken {
		return "", staleLock("stale lock token for rule group %q", in.ID)
	}

	gd.grp.Description = in.Description
	gd.grp.VisibilityConfig = in.VisibilityConfig
	gd.grp.Rules = in.Rules
	gd.grp.CustomResponses = in.CustomResponses
	gd.grp.LockToken = newLockToken()

	return gd.grp.LockToken, nil
}

// DeleteRuleGroup removes a rule group, enforcing the lock token.
func (m *Mock) DeleteRuleGroup(_ context.Context, ref driver.Ref, lockToken string) error {
	gd, err := m.getRuleGroupData(ref)
	if err != nil {
		return err
	}

	gd.mu.Lock()
	defer gd.mu.Unlock()

	if gd.grp.LockToken != lockToken {
		return staleLock("stale lock token for rule group %q", ref.ID)
	}

	if m.itemReferencedByWebACL(gd.grp.ARN) {
		return associated("rule group %q is referenced by one or more web ACLs", ref.ID)
	}

	m.ruleGrps.Delete(key(ref.Scope, ref.ID))

	return nil
}

// ListRuleGroups returns all rule groups in a scope.
func (m *Mock) ListRuleGroups(_ context.Context, scope string) ([]driver.RuleGroup, error) {
	all := m.ruleGrps.All()
	out := make([]driver.RuleGroup, 0, len(all))

	for _, gd := range all {
		gd.mu.RLock()
		if gd.grp.Scope == scope {
			out = append(out, copyRuleGroup(&gd.grp))
		}
		gd.mu.RUnlock()
	}

	return out, nil
}
