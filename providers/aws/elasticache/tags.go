package elasticache

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// resourceExists reports whether an ElastiCache ARN names a resource this mock
// knows about (a cache cluster, replication group, or parameter group). Tag
// operations on an unknown ARN return NotFound, matching AWS.
func (m *Mock) resourceExists(arn string) bool {
	name := arn
	if i := strings.LastIndex(arn, ":"); i >= 0 {
		name = arn[i+1:]
	}

	switch {
	case strings.Contains(arn, ":cluster:"):
		return m.caches.Has(name)
	case strings.Contains(arn, ":replicationgroup:"):
		return m.replicationGroups.Has(name)
	case strings.Contains(arn, ":parametergroup:"):
		return m.parameterGroups.Has(name)
	case strings.Contains(arn, ":subnetgroup:"):
		return m.subnetGroups.Has(name)
	default:
		return false
	}
}

// seedTags records the create-time tags for a resource ARN so ListTagsForResource
// returns them without a prior AddTagsToResource call.
func (m *Mock) seedTags(arn string, tags map[string]string) {
	if len(tags) == 0 {
		return
	}

	m.tagMu.Lock()
	defer m.tagMu.Unlock()

	stored := make(map[string]string, len(tags))
	for k, v := range tags {
		stored[k] = v
	}

	m.tagsByARN[arn] = stored
}

// AddTags adds or overwrites tags on an ElastiCache resource by ARN.
func (m *Mock) AddTags(_ context.Context, arn string, tags map[string]string) error {
	if !m.resourceExists(arn) {
		return cerrors.Newf(cerrors.NotFound, "cache resource %q not found", arn)
	}

	m.tagMu.Lock()
	defer m.tagMu.Unlock()

	stored := m.tagsByARN[arn]
	if stored == nil {
		stored = make(map[string]string, len(tags))
		m.tagsByARN[arn] = stored
	}

	for k, v := range tags {
		stored[k] = v
	}

	return nil
}

// ListTags returns the tags on an ElastiCache resource by ARN.
func (m *Mock) ListTags(_ context.Context, arn string) (map[string]string, error) {
	if !m.resourceExists(arn) {
		return nil, cerrors.Newf(cerrors.NotFound, "cache resource %q not found", arn)
	}

	m.tagMu.Lock()
	defer m.tagMu.Unlock()

	out := make(map[string]string, len(m.tagsByARN[arn]))
	for k, v := range m.tagsByARN[arn] {
		out[k] = v
	}

	return out, nil
}

// RemoveTags deletes the named tag keys from an ElastiCache resource by ARN.
func (m *Mock) RemoveTags(_ context.Context, arn string, keys []string) error {
	if !m.resourceExists(arn) {
		return cerrors.Newf(cerrors.NotFound, "cache resource %q not found", arn)
	}

	m.tagMu.Lock()
	defer m.tagMu.Unlock()

	stored := m.tagsByARN[arn]
	for _, k := range keys {
		delete(stored, k)
	}

	return nil
}

// CreateCacheParameterGroup registers an ElastiCache cache parameter group.
func (m *Mock) CreateCacheParameterGroup(_ context.Context, name, family, description string) (*ParameterGroup, error) {
	if name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "CacheParameterGroupName is required")
	}

	if m.parameterGroups.Has(name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "cache parameter group %q already exists", name)
	}

	pg := ParameterGroup{Name: name, Family: family, Description: description}
	m.parameterGroups.Set(name, pg)

	return &pg, nil
}

// DescribeCacheParameterGroups returns the named groups, or all when none given.
func (m *Mock) DescribeCacheParameterGroups(_ context.Context, names []string) ([]ParameterGroup, error) {
	if len(names) == 0 {
		return m.parameterGroups.SortedValues(), nil
	}

	out := make([]ParameterGroup, 0, len(names))

	for _, name := range names {
		pg, ok := m.parameterGroups.Get(name)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "cache parameter group %q not found", name)
		}

		out = append(out, pg)
	}

	return out, nil
}

// ModifyCacheParameterGroup is a no-op beyond validating the group exists —
// the emulator does not track individual parameter values, only group identity.
func (m *Mock) ModifyCacheParameterGroup(_ context.Context, name string) error {
	if !m.parameterGroups.Has(name) {
		return cerrors.Newf(cerrors.NotFound, "cache parameter group %q not found", name)
	}

	return nil
}

// DeleteCacheParameterGroup removes an ElastiCache cache parameter group.
func (m *Mock) DeleteCacheParameterGroup(_ context.Context, name string) error {
	if !m.parameterGroups.Delete(name) {
		return cerrors.Newf(cerrors.NotFound, "cache parameter group %q not found", name)
	}

	return nil
}
