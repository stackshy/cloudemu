package transfer

import "context"

// TagResource merges tags onto a resource, keyed by the caller-supplied ARN.
func (m *Mock) TagResource(_ context.Context, arn string, tags map[string]string) error {
	if arn == "" {
		return invalidRequest("Arn is required")
	}

	m.storeTags(arn, tags)

	return nil
}

// UntagResource removes the named tag keys from a resource.
func (m *Mock) UntagResource(_ context.Context, arn string, tagKeys []string) error {
	if arn == "" {
		return invalidRequest("Arn is required")
	}

	m.tagsMu.Lock()
	defer m.tagsMu.Unlock()

	existing := m.tags[arn]
	if existing == nil {
		return nil
	}

	for _, k := range tagKeys {
		delete(existing, k)
	}

	if len(existing) == 0 {
		delete(m.tags, arn)
	}

	return nil
}

// ListTagsForResource returns a resource's tags (empty when none are set).
func (m *Mock) ListTagsForResource(_ context.Context, arn string) (map[string]string, error) {
	if arn == "" {
		return nil, invalidRequest("Arn is required")
	}

	m.tagsMu.RLock()
	defer m.tagsMu.RUnlock()

	return copyStringMap(m.tags[arn]), nil
}

// storeTags merges tags under an ARN, allocating the inner map on first use.
func (m *Mock) storeTags(arn string, tags map[string]string) {
	if len(tags) == 0 {
		return
	}

	m.tagsMu.Lock()
	defer m.tagsMu.Unlock()

	existing := m.tags[arn]
	if existing == nil {
		existing = map[string]string{}
		m.tags[arn] = existing
	}

	for k, v := range tags {
		existing[k] = v
	}
}

// deleteTags drops all tags recorded for an ARN.
func (m *Mock) deleteTags(arn string) {
	m.tagsMu.Lock()
	defer m.tagsMu.Unlock()

	delete(m.tags, arn)
}
