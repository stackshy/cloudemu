package kinesis

import (
	"context"
	"sort"
)

// AddTagsToStream adds or overwrites stream tags.
func (m *Mock) AddTagsToStream(_ context.Context, name, arn string, tags map[string]string) error {
	sd, err := m.resolve(name, arn)
	if err != nil {
		return err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if sd.tags == nil {
		sd.tags = map[string]string{}
	}

	for k, v := range tags {
		sd.tags[k] = v
	}

	return nil
}

// RemoveTagsFromStream removes stream tags by key.
func (m *Mock) RemoveTagsFromStream(_ context.Context, name, arn string, keys []string) error {
	sd, err := m.resolve(name, arn)
	if err != nil {
		return err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	for _, k := range keys {
		delete(sd.tags, k)
	}

	return nil
}

// ListTagsForStream returns a stream's tags, paginated by tag key.
func (m *Mock) ListTagsForStream(
	_ context.Context, name, arn, exclusiveStartTagKey string, limit int32,
) (tags map[string]string, hasMore bool, err error) {
	sd, err := m.resolve(name, arn)
	if err != nil {
		return nil, false, err
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	keys := make([]string, 0, len(sd.tags))
	for k := range sd.tags {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	out := map[string]string{}
	started := exclusiveStartTagKey == ""
	more := false

	for _, k := range keys {
		if !started {
			if k == exclusiveStartTagKey {
				started = true
			}

			continue
		}

		if limit > 0 && len(out) == int(limit) {
			more = true
			break
		}

		out[k] = sd.tags[k]
	}

	return out, more, nil
}

// TagResource adds tags addressed by resource ARN (stream ARN).
func (m *Mock) TagResource(_ context.Context, resourceARN string, tags map[string]string) error {
	return m.AddTagsToStream(context.Background(), "", resourceARN, tags)
}

// UntagResource removes tags addressed by resource ARN.
func (m *Mock) UntagResource(_ context.Context, resourceARN string, keys []string) error {
	return m.RemoveTagsFromStream(context.Background(), "", resourceARN, keys)
}

// ListTagsForResource returns tags addressed by resource ARN.
func (m *Mock) ListTagsForResource(_ context.Context, resourceARN string) (map[string]string, error) {
	sd, err := m.resolve("", resourceARN)
	if err != nil {
		return nil, err
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	return copyTags(sd.tags), nil
}

// EnableEnhancedMonitoring turns on shard-level metrics.
func (m *Mock) EnableEnhancedMonitoring(
	_ context.Context, name, arn string, metrics []string,
) (current, desired []string, err error) {
	return m.setMonitoring(name, arn, metrics, true)
}

// DisableEnhancedMonitoring turns off shard-level metrics.
func (m *Mock) DisableEnhancedMonitoring(
	_ context.Context, name, arn string, metrics []string,
) (current, desired []string, err error) {
	return m.setMonitoring(name, arn, metrics, false)
}

func (m *Mock) setMonitoring(name, arn string, metrics []string, enable bool) (before, after []string, err error) {
	sd, err := m.resolve(name, arn)
	if err != nil {
		return nil, nil, err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	before = append([]string(nil), sd.desc.EnhancedMonitoring...)

	set := map[string]bool{}
	for _, mm := range sd.desc.EnhancedMonitoring {
		set[mm] = true
	}

	for _, mm := range metrics {
		if mm == "ALL" {
			set = map[string]bool{"ALL": enable}
			break
		}

		set[mm] = enable
	}

	after = make([]string, 0, len(set))

	for k, on := range set {
		if on {
			after = append(after, k)
		}
	}

	sort.Strings(after)
	sd.desc.EnhancedMonitoring = after

	return before, after, nil
}
