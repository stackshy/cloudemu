package bedrock

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

// TagResource merges tags onto the resource ARN's tag list, overwriting values
// for existing keys. Tagging an ARN the emulator does not track is a no-op
// success, mirroring AWS.
func (m *Mock) TagResource(_ context.Context, resourceARN string, tags []driver.Tag) error {
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

	return nil
}

// UntagResource removes the given tag keys from the resource ARN's tag list.
func (m *Mock) UntagResource(_ context.Context, resourceARN string, tagKeys []string) error {
	existing, ok := m.tags.Get(resourceARN)
	if !ok {
		return nil
	}

	remove := make(map[string]bool, len(tagKeys))
	for _, k := range tagKeys {
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

// ListTagsForResource returns a defensive copy of the resource ARN's tags in
// stable insertion order (an empty slice when none are set).
func (m *Mock) ListTagsForResource(_ context.Context, resourceARN string) ([]driver.Tag, error) {
	tags, _ := m.tags.Get(resourceARN)

	return copyTags(tags), nil
}

// setTags records the initial tags for a resource ARN (no-op for empty input).
func (m *Mock) setTags(arn string, tags []driver.Tag) {
	if len(tags) == 0 {
		return
	}

	m.tags.Set(arn, copyTags(tags))
}

// tagsFromMap converts a string map into a []driver.Tag sorted by key, giving
// deterministic ordering for create-time seeding.
func (*Mock) tagsFromMap(tags map[string]string) []driver.Tag {
	if len(tags) == 0 {
		return nil
	}

	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	out := make([]driver.Tag, 0, len(keys))
	for _, k := range keys {
		out = append(out, driver.Tag{Key: k, Value: tags[k]})
	}

	return out
}

// copyTags returns a defensive copy of tags (nil-safe, always non-nil).
func copyTags(tags []driver.Tag) []driver.Tag {
	out := make([]driver.Tag, len(tags))
	copy(out, tags)

	return out
}
