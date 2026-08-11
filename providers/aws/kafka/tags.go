package kafka

import (
	"context"
)

// Tagging resolves a ResourceArn to its owning resource and reads/merges/removes
// that resource's Tags map. Among the resources implemented so far, only the
// cluster carries a Tags map (configurations have none in MSK; VPC connections
// and replicators are not yet stored), so tag ops target cluster ARNs. An ARN
// that resolves to no known resource is a NotFoundException, matching real MSK.

// TagResource merges tags into the resource under a single write-lock hold, so a
// concurrent TagResource cannot lose an update (read-modify-write is atomic).
func (m *Mock) TagResource(_ context.Context, resourceARN string, tags map[string]string) error {
	cd, err := m.getCluster(resourceARN)
	if err != nil {
		return err
	}

	cd.mu.Lock()
	defer cd.mu.Unlock()

	if cd.cluster.Tags == nil {
		cd.cluster.Tags = make(map[string]string, len(tags))
	}

	for k, v := range tags {
		cd.cluster.Tags[k] = v
	}

	return nil
}

// ListTagsForResource returns a deep copy of the resource's tags.
func (m *Mock) ListTagsForResource(_ context.Context, resourceARN string) (map[string]string, error) {
	cd, err := m.getCluster(resourceARN)
	if err != nil {
		return nil, err
	}

	cd.mu.RLock()
	defer cd.mu.RUnlock()

	out := copyTags(cd.cluster.Tags)
	if out == nil {
		out = map[string]string{}
	}

	return out, nil
}

// UntagResource removes the given keys under a single write-lock hold.
func (m *Mock) UntagResource(_ context.Context, resourceARN string, keys []string) error {
	cd, err := m.getCluster(resourceARN)
	if err != nil {
		return err
	}

	cd.mu.Lock()
	defer cd.mu.Unlock()

	for _, k := range keys {
		delete(cd.cluster.Tags, k)
	}

	return nil
}
