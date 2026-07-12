package sagemaker

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// setTags records the initial tags for a resource ARN (no-op for empty input).
func (m *Mock) setTags(arn string, tags []driver.Tag) {
	if len(tags) == 0 {
		return
	}

	m.tags.Set(arn, copyTags(tags))
}

// AddTags merges tags onto a resource, overwriting values for existing keys.
func (m *Mock) AddTags(_ context.Context, resourceARN string, tags []driver.Tag) ([]driver.Tag, error) {
	existing, _ := m.tags.Get(resourceARN)

	merged := make([]driver.Tag, 0, len(existing)+len(tags))
	index := map[string]int{}

	for _, t := range existing {
		index[t.Key] = len(merged)
		merged = append(merged, t)
	}

	for _, t := range tags {
		if i, ok := index[t.Key]; ok {
			merged[i].Value = t.Value
			continue
		}

		index[t.Key] = len(merged)
		merged = append(merged, t)
	}

	m.tags.Set(resourceARN, merged)

	return copyTags(merged), nil
}

// ListTags returns the tags for a resource ARN.
func (m *Mock) ListTags(_ context.Context, resourceARN string) ([]driver.Tag, error) {
	tags, _ := m.tags.Get(resourceARN)

	return copyTags(tags), nil
}

// DeleteTags removes the given tag keys from a resource ARN.
func (m *Mock) DeleteTags(_ context.Context, resourceARN string, keys []string) error {
	existing, ok := m.tags.Get(resourceARN)
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

	m.tags.Set(resourceARN, kept)

	return nil
}
