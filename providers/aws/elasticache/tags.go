package elasticache

import (
	"context"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
)

// taggableResource reports whether an ElastiCache ARN names a resource this
// mock knows about (a cache cluster, replication group, parameter group, or
// subnet group), returning the resource's not-found fault when it does not.
// A cluster or replication group whose real engine is still being provisioned
// or torn down exists in AWS, so a tag write on it reports the invalid-state
// fault, while a tag read is allowed. The caller holds mu.
func (m *Mock) taggableResource(arn string, write bool) error {
	name := arn
	if i := strings.LastIndex(arn, ":"); i >= 0 {
		name = arn[i+1:]
	}

	switch {
	case strings.Contains(arn, ":cluster:"):
		return reservedOr(m.caches.Has(name), m.cacheRes[name], write,
			func(state string) error { return cacheInvalidState(name, state) }, cacheNotFound(name))
	case strings.Contains(arn, ":replicationgroup:"):
		return reservedOr(m.replicationGroups.Has(name), m.rgRes[name], write,
			func(state string) error { return rgInvalidState(name, state) }, rgNotFound(name))
	case strings.Contains(arn, ":parametergroup:"):
		if m.parameterGroups.Has(name) {
			return nil
		}

		return parameterGroupNotFound(name)
	case strings.Contains(arn, ":subnetgroup:"):
		if m.subnetGroups.Has(name) {
			return nil
		}

		return subnetGroupNotFound(name)
	default:
		// Snapshot ARNs are not taggable yet (#1695); an unknown type keeps
		// answering not-found as before.
		return newFault(cerrors.NotFound, FaultCacheClusterNotFound, "cache resource %q not found", arn)
	}
}

// reservedOr resolves a tag target that may be stored or reserved: stored is
// taggable, reserved is readable but not writable, and anything else is
// notFound.
func reservedOr[T any](stored bool, r *reservation[T], write bool, invalid func(string) error, notFound error) error {
	switch {
	case stored:
		return nil
	case r != nil && write:
		return invalid(r.state)
	case r != nil:
		return nil
	default:
		return notFound
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
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.taggableResource(arn, true); err != nil {
		return err
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
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.taggableResource(arn, false); err != nil {
		return nil, err
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
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.taggableResource(arn, true); err != nil {
		return err
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

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.lookupParameterGroup(name); exists {
		return nil, newFault(cerrors.AlreadyExists, FaultCacheParameterGroupAlreadyExists,
			"cache parameter group %q already exists", name)
	}

	pg := ParameterGroup{Name: name, Family: family, Description: description, Overrides: map[string]string{}}
	m.parameterGroups.Set(name, pg)

	return &pg, nil
}

// DescribeCacheParameterGroups returns the named groups, or all when none given.
// With no names it lists the built-in default groups too, as ElastiCache does.
func (m *Mock) DescribeCacheParameterGroups(_ context.Context, names []string) ([]ParameterGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(names) == 0 {
		out := m.parameterGroups.SortedValues()

		for _, name := range defaultGroupNames() {
			pg, _ := m.lookupParameterGroup(name)
			out = append(out, pg)
		}

		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

		return out, nil
	}

	out := make([]ParameterGroup, 0, len(names))

	for _, name := range names {
		pg, ok := m.lookupParameterGroup(name)
		if !ok {
			return nil, parameterGroupNotFound(name)
		}

		out = append(out, pg)
	}

	return out, nil
}

// DeleteCacheParameterGroup removes an ElastiCache cache parameter group.
// Real ElastiCache refuses to delete a default group or one a cluster still
// uses. The in-use scan and the delete run under mu, and clusters or groups
// still being created count as users, so a create that has validated the
// parameter group cannot commit after the group is gone.
func (m *Mock) DeleteCacheParameterGroup(_ context.Context, name string) error {
	if err := rejectDefaultGroupChange(name); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	user, inUse := m.firstUser(func(info *cachedriver.CacheInfo) bool {
		return info.ParameterGroupName == name
	}, func(rg *cachedriver.ReplicationGroup) bool {
		return rg.ParameterGroupName == name
	})
	if inUse {
		return newFault(cerrors.FailedPrecondition, FaultInvalidCacheParameterGroupState,
			"cache parameter group %q is in use by %q", name, user)
	}

	if !m.parameterGroups.Delete(name) {
		return parameterGroupNotFound(name)
	}

	return nil
}
