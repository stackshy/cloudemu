package redshift

import "context"

// CreateTags tags a Redshift resource by ARN (ResourceName). Redshift resources
// don't carry a tag field in the shared cluster model, so tags live in an
// ARN-keyed store on the provider.
func (m *Mock) CreateTags(_ context.Context, resourceName string, tags map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.tagsByARN == nil {
		m.tagsByARN = map[string]map[string]string{}
	}

	if m.tagsByARN[resourceName] == nil {
		m.tagsByARN[resourceName] = map[string]string{}
	}

	for k, v := range tags {
		m.tagsByARN[resourceName][k] = v
	}

	return nil
}

// DeleteTags removes tags by key from a Redshift resource by ARN.
func (m *Mock) DeleteTags(_ context.Context, resourceName string, keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, k := range keys {
		delete(m.tagsByARN[resourceName], k)
	}

	return nil
}

// DescribeTags returns the tags on a Redshift resource by ARN.
func (m *Mock) DescribeTags(_ context.Context, resourceName string) (map[string]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make(map[string]string, len(m.tagsByARN[resourceName]))
	for k, v := range m.tagsByARN[resourceName] {
		out[k] = v
	}

	return out, nil
}
