package iam

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/errors"
)

// PutUserPolicy adds or replaces an inline policy on a user (IAM
// PutUserPolicy). Inline policies are embedded in the user, distinct from the
// managed policies attached via AttachUserPolicy.
func (m *Mock) PutUserPolicy(_ context.Context, userName, policyName, policyDocument string) error {
	if !m.users.Has(userName) {
		return errors.Newf(errors.NotFound, "user %q not found", userName)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.userInlinePolicies[userName] == nil {
		m.userInlinePolicies[userName] = make(map[string]string)
	}

	m.userInlinePolicies[userName][policyName] = policyDocument

	return nil
}

// GetUserPolicy returns an inline policy document by name (IAM GetUserPolicy).
func (m *Mock) GetUserPolicy(_ context.Context, userName, policyName string) (string, error) {
	if !m.users.Has(userName) {
		return "", errors.Newf(errors.NotFound, "user %q not found", userName)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	doc, ok := m.userInlinePolicies[userName][policyName]
	if !ok {
		return "", errors.Newf(errors.NotFound, "policy %q not found on user %q", policyName, userName)
	}

	return doc, nil
}

// DeleteUserPolicy removes an inline policy from a user (IAM DeleteUserPolicy).
func (m *Mock) DeleteUserPolicy(_ context.Context, userName, policyName string) error {
	if !m.users.Has(userName) {
		return errors.Newf(errors.NotFound, "user %q not found", userName)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.userInlinePolicies[userName][policyName]; !ok {
		return errors.Newf(errors.NotFound, "policy %q not found on user %q", policyName, userName)
	}

	delete(m.userInlinePolicies[userName], policyName)

	return nil
}

// ListUserPolicies returns the names of a user's inline policies, sorted (IAM
// ListUserPolicies).
func (m *Mock) ListUserPolicies(_ context.Context, userName string) ([]string, error) {
	if !m.users.Has(userName) {
		return nil, errors.Newf(errors.NotFound, "user %q not found", userName)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.userInlinePolicies[userName]))
	for name := range m.userInlinePolicies[userName] {
		names = append(names, name)
	}

	sort.Strings(names)

	return names, nil
}
