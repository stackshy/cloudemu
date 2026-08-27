package iam

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
)

// TagUser adds or overwrites tags on a user (IAM TagUser). It exists for the
// wire layer and is not part of the portable driver.
func (m *Mock) TagUser(_ context.Context, userName string, tags map[string]string) error {
	u, ok := m.users.Get(userName)
	if !ok {
		return errors.Newf(errors.NotFound, "user %q not found", userName)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if u.Tags == nil {
		u.Tags = make(map[string]string, len(tags))
	}

	for k, v := range tags {
		u.Tags[k] = v
	}

	return nil
}

// UntagUser removes tags by key from a user (IAM UntagUser). It exists for the
// wire layer and is not part of the portable driver.
func (m *Mock) UntagUser(_ context.Context, userName string, keys []string) error {
	u, ok := m.users.Get(userName)
	if !ok {
		return errors.Newf(errors.NotFound, "user %q not found", userName)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, k := range keys {
		delete(u.Tags, k)
	}

	return nil
}

// ListUserTags returns a user's tags (IAM ListUserTags). It exists for the wire
// layer and is not part of the portable driver.
func (m *Mock) ListUserTags(_ context.Context, userName string) (map[string]string, error) {
	u, ok := m.users.Get(userName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "user %q not found", userName)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make(map[string]string, len(u.Tags))
	for k, v := range u.Tags {
		out[k] = v
	}

	return out, nil
}
