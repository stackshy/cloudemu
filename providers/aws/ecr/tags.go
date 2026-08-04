package ecr

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
)

// TagRepository adds or overwrites tags on a repository (ECR TagResource).
func (m *Mock) TagRepository(_ context.Context, name string, tags map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.repos.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "repository %q not found", name)
	}

	if rd.info.Tags == nil {
		rd.info.Tags = make(map[string]string, len(tags))
	}

	for k, v := range tags {
		rd.info.Tags[k] = v
	}

	return nil
}

// UntagRepository removes tags by key from a repository (ECR UntagResource).
func (m *Mock) UntagRepository(_ context.Context, name string, keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.repos.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "repository %q not found", name)
	}

	for _, k := range keys {
		delete(rd.info.Tags, k)
	}

	return nil
}

// ListRepositoryTags returns a repository's tags (ECR ListTagsForResource).
func (m *Mock) ListRepositoryTags(_ context.Context, name string) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.repos.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "repository %q not found", name)
	}

	out := make(map[string]string, len(rd.info.Tags))
	for k, v := range rd.info.Tags {
		out[k] = v
	}

	return out, nil
}
