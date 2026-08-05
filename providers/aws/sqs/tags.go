package sqs

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
)

// TagQueue adds or overwrites tags on a queue (SQS TagQueue).
func (m *Mock) TagQueue(_ context.Context, queueURL string, tags map[string]string) error {
	qd, ok := m.queues.Get(queueURL)
	if !ok {
		return errors.Newf(errors.NotFound, "queue %q not found", queueURL)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	if qd.info.Tags == nil {
		qd.info.Tags = make(map[string]string, len(tags))
	}

	for k, v := range tags {
		qd.info.Tags[k] = v
	}

	return nil
}

// UntagQueue removes tags by key from a queue (SQS UntagQueue).
func (m *Mock) UntagQueue(_ context.Context, queueURL string, keys []string) error {
	qd, ok := m.queues.Get(queueURL)
	if !ok {
		return errors.Newf(errors.NotFound, "queue %q not found", queueURL)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	for _, k := range keys {
		delete(qd.info.Tags, k)
	}

	return nil
}

// ListQueueTags returns a queue's tags (SQS ListQueueTags).
func (m *Mock) ListQueueTags(_ context.Context, queueURL string) (map[string]string, error) {
	qd, ok := m.queues.Get(queueURL)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "queue %q not found", queueURL)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	out := make(map[string]string, len(qd.info.Tags))
	for k, v := range qd.info.Tags {
		out[k] = v
	}

	return out, nil
}
