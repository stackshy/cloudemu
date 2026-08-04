package ssm

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
)

// TagParameter adds or overwrites tags on a parameter (SSM AddTagsToResource
// with ResourceType=Parameter).
func (m *Mock) TagParameter(_ context.Context, name string, tags map[string]string) error {
	pd, ok := m.params.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "parameter %q not found", name)
	}

	pd.mu.Lock()
	defer pd.mu.Unlock()

	if pd.tags == nil {
		pd.tags = make(map[string]string, len(tags))
	}

	for k, v := range tags {
		pd.tags[k] = v
	}

	return nil
}

// UntagParameter removes tags by key from a parameter (SSM
// RemoveTagsFromResource).
func (m *Mock) UntagParameter(_ context.Context, name string, keys []string) error {
	pd, ok := m.params.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "parameter %q not found", name)
	}

	pd.mu.Lock()
	defer pd.mu.Unlock()

	for _, k := range keys {
		delete(pd.tags, k)
	}

	return nil
}

// ListParameterTags returns a parameter's tags (SSM ListTagsForResource).
func (m *Mock) ListParameterTags(_ context.Context, name string) (map[string]string, error) {
	pd, ok := m.params.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "parameter %q not found", name)
	}

	pd.mu.RLock()
	defer pd.mu.RUnlock()

	out := make(map[string]string, len(pd.tags))
	for k, v := range pd.tags {
		out[k] = v
	}

	return out, nil
}
