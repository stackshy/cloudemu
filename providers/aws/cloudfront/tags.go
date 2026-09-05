package cloudfront

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/cloudfront/driver"
)

// ListTagsForResource returns the tags on the distribution named by arn.
func (m *Mock) ListTagsForResource(_ context.Context, arn string) (map[string]string, error) {
	dist, err := m.distByARN(arn)
	if err != nil {
		return nil, err
	}

	return cloneTags(dist.Tags), nil
}

// TagResource merges tags onto the distribution named by arn.
func (m *Mock) TagResource(_ context.Context, arn string, tags map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idFromARN(arn)

	dist, ok := m.dists.Get(id)
	if !ok {
		return driver.ErrNoSuchDistribution
	}

	merged := cloneTags(dist.Tags)
	if merged == nil {
		merged = map[string]string{}
	}

	for k, v := range tags {
		merged[k] = v
	}

	dist.Tags = merged
	m.dists.Set(id, dist)

	return nil
}

// UntagResource removes the named tag keys from the distribution named by arn.
func (m *Mock) UntagResource(_ context.Context, arn string, keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idFromARN(arn)

	dist, ok := m.dists.Get(id)
	if !ok {
		return driver.ErrNoSuchDistribution
	}

	merged := cloneTags(dist.Tags)
	for _, k := range keys {
		delete(merged, k)
	}

	dist.Tags = merged
	m.dists.Set(id, dist)

	return nil
}

// distByARN resolves a distribution from its ARN.
func (m *Mock) distByARN(arn string) (driver.Distribution, error) {
	dist, ok := m.dists.Get(idFromARN(arn))
	if !ok {
		return driver.Distribution{}, driver.ErrNoSuchDistribution
	}

	return dist, nil
}
