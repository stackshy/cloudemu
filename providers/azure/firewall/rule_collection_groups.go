package firewall

import (
	"context"
	"slices"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/azurefirewall/driver"
)

// Real Azure accepts rule collection group and rule collection priorities in
// this range.
const (
	minRulePriority = 100
	maxRulePriority = 65000
)

// groupKey keys a rule collection group under its policy.
func groupKey(rg, policy, name string) string {
	return key(rg, policy) + "/" + strings.ToLower(name)
}

// CreateOrUpdateRuleCollectionGroup stores g under the policy as a full
// replace.
func (m *Mock) CreateOrUpdateRuleCollectionGroup(
	_ context.Context, rg, policy, name string, g driver.RuleCollectionGroup,
) (*driver.RuleCollectionGroup, bool, error) {
	if _, ok := m.policies.Get(key(rg, policy)); !ok {
		return nil, false, cerrors.Newf(cerrors.NotFound, "firewall policy %q not found", policy)
	}

	if err := validateGroupPriorities(&g); err != nil {
		return nil, false, err
	}

	k := groupKey(rg, policy, name)
	_, existed := m.groups.Get(k)

	stored := cloneGroup(g)
	stored.Name = name
	m.groups.Set(k, stored)

	out := cloneGroup(stored)

	return &out, !existed, nil
}

func validateGroupPriorities(g *driver.RuleCollectionGroup) error {
	if g.Priority < minRulePriority || g.Priority > maxRulePriority {
		return cerrors.Newf(cerrors.InvalidArgument,
			"rule collection group priority %d must be between %d and %d", g.Priority, minRulePriority, maxRulePriority)
	}

	names := map[string]bool{}

	for _, rc := range g.RuleCollections {
		obj, _ := rc.(map[string]any)
		name, _ := obj["name"].(string)

		if names[strings.ToLower(name)] {
			return cerrors.Newf(cerrors.InvalidArgument, "rule collection name %q is not unique in the group", name)
		}

		names[strings.ToLower(name)] = true

		p, ok := obj["priority"].(float64)
		if !ok || p < minRulePriority || p > maxRulePriority {
			return cerrors.Newf(cerrors.InvalidArgument,
				"rule collection %q priority must be between %d and %d", name, minRulePriority, maxRulePriority)
		}
	}

	return nil
}

// GetRuleCollectionGroup returns one group of a policy.
func (m *Mock) GetRuleCollectionGroup(_ context.Context, rg, policy, name string) (*driver.RuleCollectionGroup, error) {
	g, ok := m.groups.Get(groupKey(rg, policy, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "rule collection group %q not found", name)
	}

	out := cloneGroup(g)

	return &out, nil
}

// DeleteRuleCollectionGroup removes one group of a policy.
func (m *Mock) DeleteRuleCollectionGroup(_ context.Context, rg, policy, name string) error {
	if !m.groups.Delete(groupKey(rg, policy, name)) {
		return cerrors.Newf(cerrors.NotFound, "rule collection group %q not found", name)
	}

	return nil
}

// ListRuleCollectionGroups returns a policy's groups sorted by name.
func (m *Mock) ListRuleCollectionGroups(_ context.Context, rg, policy string) ([]driver.RuleCollectionGroup, error) {
	if _, ok := m.policies.Get(key(rg, policy)); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "firewall policy %q not found", policy)
	}

	prefix := key(rg, policy) + "/"
	out := []driver.RuleCollectionGroup{}

	keys := m.groups.Keys()
	slices.Sort(keys)

	for _, k := range keys {
		if g, ok := m.groups.Get(k); ok && strings.HasPrefix(k, prefix) {
			out = append(out, cloneGroup(g))
		}
	}

	return out, nil
}

// dropGroups removes every rule collection group of a deleted policy.
func (m *Mock) dropGroups(rg, policy string) {
	prefix := key(rg, policy) + "/"

	for _, k := range m.groups.Keys() {
		if strings.HasPrefix(k, prefix) {
			m.groups.Delete(k)
		}
	}
}

func cloneGroup(g driver.RuleCollectionGroup) driver.RuleCollectionGroup {
	out := g
	out.RuleCollections = make([]any, len(g.RuleCollections))

	for i, rc := range g.RuleCollections {
		out.RuleCollections[i] = cloneAnyValue(rc)
	}

	return out
}
