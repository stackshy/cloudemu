package keyspaces

import (
	"context"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	ksdriver "github.com/stackshy/cloudemu/v2/services/keyspaces/driver"
)

// setTags merges tags onto a resource ARN. The caller holds the write lock.
func (m *Mock) setTags(arn string, tags map[string]string) {
	if len(tags) == 0 {
		return
	}

	cur := m.tags[arn]
	if cur == nil {
		cur = make(map[string]string, len(tags))
	}

	for k, v := range tags {
		cur[k] = v
	}

	m.tags[arn] = cur
}

// TagResource adds tags to a resource by ARN.
func (m *Mock) TagResource(_ context.Context, arn string, tags map[string]string) error {
	if arn == "" {
		return cerrors.New(cerrors.InvalidArgument, "resourceArn is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.setTags(arn, tags)

	return nil
}

// UntagResource removes tag keys from a resource by ARN.
func (m *Mock) UntagResource(_ context.Context, arn string, keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cur := m.tags[arn]
	for _, k := range keys {
		delete(cur, k)
	}

	return nil
}

// ListTagsForResource returns a resource's tags in deterministic (sorted)
// order (AWS leaves tag order unspecified).
func (m *Mock) ListTagsForResource(_ context.Context, arn string) ([]ksdriver.Tag, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tags := m.tags[arn]

	names := make([]string, 0, len(tags))
	for k := range tags {
		names = append(names, k)
	}

	sort.Strings(names)

	out := make([]ksdriver.Tag, 0, len(names))
	for _, k := range names {
		out = append(out, ksdriver.Tag{Key: k, Value: tags[k]})
	}

	return out, nil
}
