package awsiam

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/errors"
)

// PutRolePolicy adds or replaces an inline policy on a role (IAM
// PutRolePolicy). Inline policies are embedded in the role, distinct from the
// managed policies attached via AttachRolePolicy.
func (m *Mock) PutRolePolicy(_ context.Context, roleName, policyName, policyDocument string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.roles.Get(roleName)
	if !ok {
		return errors.Newf(errors.NotFound, "role %q not found", roleName)
	}

	if rd.inlinePolicies == nil {
		rd.inlinePolicies = make(map[string]string)
	}

	rd.inlinePolicies[policyName] = policyDocument

	return nil
}

// GetRolePolicy returns an inline policy document by name (IAM GetRolePolicy).
func (m *Mock) GetRolePolicy(_ context.Context, roleName, policyName string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rd, ok := m.roles.Get(roleName)
	if !ok {
		return "", errors.Newf(errors.NotFound, "role %q not found", roleName)
	}

	doc, ok := rd.inlinePolicies[policyName]
	if !ok {
		return "", errors.Newf(errors.NotFound, "policy %q not found on role %q", policyName, roleName)
	}

	return doc, nil
}

// DeleteRolePolicy removes an inline policy from a role (IAM DeleteRolePolicy).
func (m *Mock) DeleteRolePolicy(_ context.Context, roleName, policyName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.roles.Get(roleName)
	if !ok {
		return errors.Newf(errors.NotFound, "role %q not found", roleName)
	}

	if _, ok := rd.inlinePolicies[policyName]; !ok {
		return errors.Newf(errors.NotFound, "policy %q not found on role %q", policyName, roleName)
	}

	delete(rd.inlinePolicies, policyName)

	return nil
}

// ListRolePolicies returns the names of a role's inline policies, sorted (IAM
// ListRolePolicies).
func (m *Mock) ListRolePolicies(_ context.Context, roleName string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rd, ok := m.roles.Get(roleName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "role %q not found", roleName)
	}

	names := make([]string, 0, len(rd.inlinePolicies))
	for name := range rd.inlinePolicies {
		names = append(names, name)
	}

	sort.Strings(names)

	return names, nil
}
