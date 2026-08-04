package eventbridge

import (
	"context"
	"sync"
)

// tagStore is a generic ARN-keyed tag store. EventBridge tags rules and event
// buses by ARN; rules carry no tag field of their own, so a shared store keyed
// by ARN backs TagResource/UntagResource/ListTagsForResource uniformly.
type tagStore struct {
	mu   sync.RWMutex
	tags map[string]map[string]string // ARN -> tags
}

func (t *tagStore) tag(arn string, tags map[string]string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.tags == nil {
		t.tags = map[string]map[string]string{}
	}

	if t.tags[arn] == nil {
		t.tags[arn] = map[string]string{}
	}

	for k, v := range tags {
		t.tags[arn][k] = v
	}
}

func (t *tagStore) untag(arn string, keys []string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, k := range keys {
		delete(t.tags[arn], k)
	}
}

func (t *tagStore) list(arn string) map[string]string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	out := make(map[string]string, len(t.tags[arn]))
	for k, v := range t.tags[arn] {
		out[k] = v
	}

	return out
}

// TagResource tags an EventBridge resource (rule or event bus) by ARN.
func (m *Mock) TagResource(_ context.Context, arn string, tags map[string]string) error {
	m.tagsByARN.tag(arn, tags)

	return nil
}

// UntagResource removes tags by key from an EventBridge resource by ARN.
func (m *Mock) UntagResource(_ context.Context, arn string, keys []string) error {
	m.tagsByARN.untag(arn, keys)

	return nil
}

// ListResourceTags returns the tags on an EventBridge resource by ARN.
func (m *Mock) ListResourceTags(_ context.Context, arn string) (map[string]string, error) {
	return m.tagsByARN.list(arn), nil
}
