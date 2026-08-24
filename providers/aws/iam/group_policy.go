package iam

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/errors"
)

// AttachGroupPolicy attaches a managed policy to a group (IAM
// AttachGroupPolicy).
func (m *Mock) AttachGroupPolicy(_ context.Context, groupName, policyARN string) error {
	return m.attachPolicy(m.groups, groupName, policyARN, m.groupPolicies, "group")
}

// DetachGroupPolicy detaches a managed policy from a group (IAM
// DetachGroupPolicy).
func (m *Mock) DetachGroupPolicy(_ context.Context, groupName, policyARN string) error {
	if !m.groups.Has(groupName) {
		return errors.Newf(errors.NotFound, "group %q not found", groupName)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	policies, ok := m.groupPolicies[groupName]
	if !ok || !policies[policyARN] {
		return errors.Newf(errors.NotFound, "policy %q is not attached to group %q", policyARN, groupName)
	}

	delete(policies, policyARN)

	return nil
}

// ListAttachedGroupPolicies returns the ARNs of managed policies attached to
// the given group (IAM ListAttachedGroupPolicies).
func (m *Mock) ListAttachedGroupPolicies(_ context.Context, groupName string) ([]string, error) {
	if !m.groups.Has(groupName) {
		return nil, errors.Newf(errors.NotFound, "group %q not found", groupName)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	policies := m.groupPolicies[groupName]
	result := make([]string, 0, len(policies))

	for arn := range policies {
		result = append(result, arn)
	}

	sort.Strings(result)

	return result, nil
}

// PutGroupPolicy adds or replaces an inline policy on a group (IAM
// PutGroupPolicy).
func (m *Mock) PutGroupPolicy(_ context.Context, groupName, policyName, policyDocument string) error {
	if !m.groups.Has(groupName) {
		return errors.Newf(errors.NotFound, "group %q not found", groupName)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.groupInlinePolicies[groupName] == nil {
		m.groupInlinePolicies[groupName] = make(map[string]string)
	}

	m.groupInlinePolicies[groupName][policyName] = policyDocument

	return nil
}

// GetGroupPolicy returns an inline policy document by name (IAM GetGroupPolicy).
func (m *Mock) GetGroupPolicy(_ context.Context, groupName, policyName string) (string, error) {
	if !m.groups.Has(groupName) {
		return "", errors.Newf(errors.NotFound, "group %q not found", groupName)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	doc, ok := m.groupInlinePolicies[groupName][policyName]
	if !ok {
		return "", errors.Newf(errors.NotFound, "policy %q not found on group %q", policyName, groupName)
	}

	return doc, nil
}

// DeleteGroupPolicy removes an inline policy from a group (IAM
// DeleteGroupPolicy).
func (m *Mock) DeleteGroupPolicy(_ context.Context, groupName, policyName string) error {
	if !m.groups.Has(groupName) {
		return errors.Newf(errors.NotFound, "group %q not found", groupName)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.groupInlinePolicies[groupName][policyName]; !ok {
		return errors.Newf(errors.NotFound, "policy %q not found on group %q", policyName, groupName)
	}

	delete(m.groupInlinePolicies[groupName], policyName)

	return nil
}

// ListGroupPolicies returns the names of a group's inline policies, sorted (IAM
// ListGroupPolicies).
func (m *Mock) ListGroupPolicies(_ context.Context, groupName string) ([]string, error) {
	if !m.groups.Has(groupName) {
		return nil, errors.Newf(errors.NotFound, "group %q not found", groupName)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.groupInlinePolicies[groupName]))
	for name := range m.groupInlinePolicies[groupName] {
		names = append(names, name)
	}

	sort.Strings(names)

	return names, nil
}
