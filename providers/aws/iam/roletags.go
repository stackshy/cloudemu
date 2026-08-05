package iam

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
)

// TagRole adds or overwrites tags on a role (IAM TagRole).
func (m *Mock) TagRole(_ context.Context, roleName string, tags map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.roles.Get(roleName)
	if !ok {
		return errors.Newf(errors.NotFound, "role %q not found", roleName)
	}

	if rd.Tags == nil {
		rd.Tags = make(map[string]string, len(tags))
	}

	for k, v := range tags {
		rd.Tags[k] = v
	}

	return nil
}

// UntagRole removes tags by key from a role (IAM UntagRole).
func (m *Mock) UntagRole(_ context.Context, roleName string, keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.roles.Get(roleName)
	if !ok {
		return errors.Newf(errors.NotFound, "role %q not found", roleName)
	}

	for _, k := range keys {
		delete(rd.Tags, k)
	}

	return nil
}

// ListRoleTags returns a role's tags (IAM ListRoleTags).
func (m *Mock) ListRoleTags(_ context.Context, roleName string) (map[string]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rd, ok := m.roles.Get(roleName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "role %q not found", roleName)
	}

	out := make(map[string]string, len(rd.Tags))
	for k, v := range rd.Tags {
		out[k] = v
	}

	return out, nil
}
