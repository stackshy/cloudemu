package vpclattice

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

func (m *Mock) TagResource(_ context.Context, arn string, tags map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cur, ok := m.tags.Get(arn)
	if !ok {
		cur = map[string]string{}
	}

	for k, v := range tags {
		cur[k] = v
	}

	m.tags.Set(arn, cur)

	return nil
}

func (m *Mock) UntagResource(_ context.Context, arn string, keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cur, ok := m.tags.Get(arn)
	if !ok {
		return nil
	}

	for _, k := range keys {
		delete(cur, k)
	}

	m.tags.Set(arn, cur)

	return nil
}

func (m *Mock) ListTagsForResource(_ context.Context, arn string) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cur, ok := m.tags.Get(arn)
	if !ok {
		return map[string]string{}, nil
	}

	out := make(map[string]string, len(cur))
	for k, v := range cur {
		out[k] = v
	}

	return out, nil
}

// compile-time assertion the tagging surface satisfies the driver contract.
var _ driver.Tagging = (*Mock)(nil)
