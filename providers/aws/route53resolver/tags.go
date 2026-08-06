package route53resolver

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/route53resolver/driver"
)

func tagResourceNotFound(arn string) error {
	return errors.Newf(errors.NotFound, "resource %q not found", arn)
}

func (m *Mock) TagResource(_ context.Context, arn string, tags []driver.Tag) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.arnExists(arn) {
		return tagResourceNotFound(arn)
	}

	existing, _ := m.tags.Get(arn)
	m.tags.Set(arn, mergeTags(existing, tags))

	return nil
}

func (m *Mock) UntagResource(_ context.Context, arn string, keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.arnExists(arn) {
		return tagResourceNotFound(arn)
	}

	existing, ok := m.tags.Get(arn)
	if !ok {
		return nil
	}

	remove := make(map[string]bool, len(keys))
	for _, k := range keys {
		remove[k] = true
	}

	kept := make([]driver.Tag, 0, len(existing))

	for _, t := range existing {
		if !remove[t.Key] {
			kept = append(kept, t)
		}
	}

	m.tags.Set(arn, kept)

	return nil
}

func (m *Mock) ListTagsForResource(_ context.Context, arn string) ([]driver.Tag, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.arnExists(arn) {
		return nil, tagResourceNotFound(arn)
	}

	existing, _ := m.tags.Get(arn)

	return copyTags(existing), nil
}

// mergeTags overlays incoming tags onto existing ones, overwriting by key and
// preserving first-seen order (AWS tag semantics).
func mergeTags(existing, incoming []driver.Tag) []driver.Tag {
	idx := make(map[string]int, len(existing))
	merged := make([]driver.Tag, 0, len(existing)+len(incoming))

	for _, t := range existing {
		idx[t.Key] = len(merged)
		merged = append(merged, t)
	}

	for _, t := range incoming {
		if i, ok := idx[t.Key]; ok {
			merged[i].Value = t.Value

			continue
		}

		idx[t.Key] = len(merged)
		merged = append(merged, t)
	}

	return merged
}
