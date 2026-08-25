package iam

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// ListEntitiesForPolicy returns the users, groups, and roles the managed policy
// with the given ARN is attached to (IAM ListEntitiesForPolicy). It reverse-
// scans the managed-policy attachment maps and resolves each principal's
// Name/Id/Path from its store. The wire layer applies the EntityFilter,
// PathPrefix, and pagination filters. AWS-only; not part of the portable driver.
func (m *Mock) ListEntitiesForPolicy(_ context.Context, policyARN string) (driver.PolicyEntities, error) {
	if !m.policies.Has(policyARN) {
		return driver.PolicyEntities{}, errors.Newf(errors.NotFound, "policy %q not found", policyARN)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	return driver.PolicyEntities{
		Users:  m.attachedUsersLocked(policyARN),
		Groups: m.attachedGroupsLocked(policyARN),
		Roles:  m.attachedRolesLocked(policyARN),
	}, nil
}

// attachedUsersLocked returns the users the policy ARN is attached to. Caller
// must hold m.mu.
func (m *Mock) attachedUsersLocked(policyARN string) []driver.PolicyEntity {
	var out []driver.PolicyEntity

	for name, arns := range m.userPolicies {
		if arns[policyARN] {
			if u, ok := m.users.Get(name); ok {
				out = append(out, driver.PolicyEntity{Name: u.Name, ID: u.ID, Path: u.Path})
			}
		}
	}

	return out
}

// attachedGroupsLocked returns the groups the policy ARN is attached to. Caller
// must hold m.mu.
func (m *Mock) attachedGroupsLocked(policyARN string) []driver.PolicyEntity {
	var out []driver.PolicyEntity

	for name, arns := range m.groupPolicies {
		if arns[policyARN] {
			if g, ok := m.groups.Get(name); ok {
				out = append(out, driver.PolicyEntity{Name: g.Name, ID: g.ID, Path: g.Path})
			}
		}
	}

	return out
}

// attachedRolesLocked returns the roles the policy ARN is attached to. Caller
// must hold m.mu.
func (m *Mock) attachedRolesLocked(policyARN string) []driver.PolicyEntity {
	var out []driver.PolicyEntity

	for name, arns := range m.rolePolicies {
		if arns[policyARN] {
			if r, ok := m.roles.Get(name); ok {
				out = append(out, driver.PolicyEntity{Name: r.Name, ID: r.ID, Path: r.Path})
			}
		}
	}

	return out
}
