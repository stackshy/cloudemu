package cloudtrail

import "context"

// storeResourceTags overwrites the tag set for a resource ARN. A nil/empty map
// clears the entry.
func (m *Mock) storeResourceTags(resourceID string, tags map[string]string) {
	m.tagsMu.Lock()
	defer m.tagsMu.Unlock()

	if len(tags) == 0 {
		delete(m.tags, resourceID)

		return
	}

	m.tags[resourceID] = copyTags(tags)
}

func (m *Mock) deleteResourceTags(resourceID string) {
	m.tagsMu.Lock()
	defer m.tagsMu.Unlock()

	delete(m.tags, resourceID)
}

// AddTags adds or overwrites tags on a resource (trail/EDS/channel/dashboard).
func (m *Mock) AddTags(_ context.Context, resourceID string, tags map[string]string) error {
	if resourceID == "" {
		return errInvalidParameter("ResourceId is required")
	}

	m.tagsMu.Lock()
	defer m.tagsMu.Unlock()

	if m.tags[resourceID] == nil {
		m.tags[resourceID] = map[string]string{}
	}

	for k, v := range tags {
		m.tags[resourceID][k] = v
	}

	return nil
}

// RemoveTags removes tags by key from a resource.
func (m *Mock) RemoveTags(_ context.Context, resourceID string, tagKeys []string) error {
	if resourceID == "" {
		return errInvalidParameter("ResourceId is required")
	}

	m.tagsMu.Lock()
	defer m.tagsMu.Unlock()

	for _, k := range tagKeys {
		delete(m.tags[resourceID], k)
	}

	if len(m.tags[resourceID]) == 0 {
		delete(m.tags, resourceID)
	}

	return nil
}

// ListTags returns a copy of the tags for each requested resource ID.
func (m *Mock) ListTags(_ context.Context, resourceIDs []string) (map[string]map[string]string, error) {
	m.tagsMu.RLock()
	defer m.tagsMu.RUnlock()

	out := make(map[string]map[string]string, len(resourceIDs))
	for _, id := range resourceIDs {
		out[id] = copyTags(m.tags[id])
	}

	return out, nil
}
