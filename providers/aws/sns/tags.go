package sns

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
)

// TagTopic adds or overwrites tags on a topic (SNS TagResource).
func (m *Mock) TagTopic(_ context.Context, topicName string, tags map[string]string) error {
	td, ok := m.topics.Get(topicName)
	if !ok {
		return errors.Newf(errors.NotFound, "topic %q not found", topicName)
	}

	td.mu.Lock()
	defer td.mu.Unlock()

	if td.info.Tags == nil {
		td.info.Tags = make(map[string]string, len(tags))
	}

	for k, v := range tags {
		td.info.Tags[k] = v
	}

	return nil
}

// UntagTopic removes tags by key from a topic (SNS UntagResource).
func (m *Mock) UntagTopic(_ context.Context, topicName string, keys []string) error {
	td, ok := m.topics.Get(topicName)
	if !ok {
		return errors.Newf(errors.NotFound, "topic %q not found", topicName)
	}

	td.mu.Lock()
	defer td.mu.Unlock()

	for _, k := range keys {
		delete(td.info.Tags, k)
	}

	return nil
}

// ListTopicTags returns a topic's tags (SNS ListTagsForResource).
func (m *Mock) ListTopicTags(_ context.Context, topicName string) (map[string]string, error) {
	td, ok := m.topics.Get(topicName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "topic %q not found", topicName)
	}

	td.mu.RLock()
	defer td.mu.RUnlock()

	out := make(map[string]string, len(td.info.Tags))
	for k, v := range td.info.Tags {
		out[k] = v
	}

	return out, nil
}
