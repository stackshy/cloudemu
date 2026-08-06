package memorydb

import (
	"context"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
)

// defaultParameters is a representative subset of the MemoryDB parameter
// catalog, keyed by name with its default value.
//
//nolint:gochecknoglobals // immutable default-parameter catalog.
var defaultParameters = map[string]mdbdriver.Parameter{
	"maxmemory-policy": {
		Name: "maxmemory-policy", Value: "noeviction", DataType: "string",
		AllowedValues:        "noeviction,allkeys-lru,volatile-lru,allkeys-lfu,volatile-lfu,allkeys-random,volatile-random,volatile-ttl",
		MinimumEngineVersion: "6.2",
	},
	"timeout":           {Name: "timeout", Value: "0", DataType: "integer", AllowedValues: "0-", MinimumEngineVersion: "6.2"},
	"maxmemory-samples": {Name: "maxmemory-samples", Value: "3", DataType: "integer", AllowedValues: "1-", MinimumEngineVersion: "6.2"},
	"activedefrag":      {Name: "activedefrag", Value: "no", DataType: "string", AllowedValues: "yes,no", MinimumEngineVersion: "6.2"},
	"tcp-keepalive":     {Name: "tcp-keepalive", Value: "300", DataType: "integer", AllowedValues: "0-", MinimumEngineVersion: "6.2"},
}

func cloneParameterGroup(in *mdbdriver.ParameterGroup) mdbdriver.ParameterGroup {
	p := *in
	p.Tags = copyTags(p.Tags)

	return p
}

// clusterUsesParameterGroup reports whether any cluster references the group.
// The caller holds the lock.
func (m *Mock) clusterUsesParameterGroup(name string) bool {
	all := m.clusters.SortedValues()
	for i := range all {
		if all[i].ParameterGroupName == name {
			return true
		}
	}

	return false
}

// CreateParameterGroup creates a parameter group.
func (m *Mock) CreateParameterGroup(
	_ context.Context, name, family, description string, tags map[string]string,
) (*mdbdriver.ParameterGroup, error) {
	if err := validName("parameter group", name); err != nil {
		return nil, err
	}

	if family == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "family is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.parameterGroups.Has(name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "parameter group %q already exists", name)
	}

	pg := mdbdriver.ParameterGroup{
		Name: name, ARN: m.arn("parametergroup", name),
		Family: family, Description: description, Tags: copyTags(tags),
	}
	m.parameterGroups.Set(name, pg)

	out := cloneParameterGroup(&pg)

	return &out, nil
}

// DescribeParameterGroups returns all parameter groups, or the named ones.
func (m *Mock) DescribeParameterGroups(_ context.Context, names []string) ([]mdbdriver.ParameterGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeByName(m.parameterGroups, names, cloneParameterGroup, func(n string) error {
		return cerrors.Newf(cerrors.NotFound, "parameter group %q not found", n)
	})
}

// UpdateParameterGroup applies parameter overrides.
func (m *Mock) UpdateParameterGroup(
	_ context.Context, name string, params []mdbdriver.ParameterNameValue,
) (*mdbdriver.ParameterGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pg, ok := m.parameterGroups.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "parameter group %q not found", name)
	}

	for _, p := range params {
		if _, known := defaultParameters[p.Name]; !known {
			return nil, cerrors.Newf(cerrors.InvalidArgument, "unknown parameter %q", p.Name)
		}
	}

	if m.paramOverrides[name] == nil {
		m.paramOverrides[name] = make(map[string]string)
	}

	for _, p := range params {
		m.paramOverrides[name][p.Name] = p.Value
	}

	out := cloneParameterGroup(&pg)

	return &out, nil
}

// ResetParameterGroup clears all overrides (or the named ones).
func (m *Mock) ResetParameterGroup(_ context.Context, name string, all bool, names []string) (*mdbdriver.ParameterGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pg, ok := m.parameterGroups.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "parameter group %q not found", name)
	}

	if all {
		delete(m.paramOverrides, name)
	} else {
		for _, n := range names {
			delete(m.paramOverrides[name], n)
		}
	}

	out := cloneParameterGroup(&pg)

	return &out, nil
}

// DeleteParameterGroup removes a parameter group; defaults and in-use groups
// cannot be deleted.
func (m *Mock) DeleteParameterGroup(_ context.Context, name string) (*mdbdriver.ParameterGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pg, ok := m.parameterGroups.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "parameter group %q not found", name)
	}

	if len(name) >= len("default.") && name[:len("default.")] == "default." {
		return nil, cerrors.New(cerrors.InvalidArgument, "cannot delete a default parameter group")
	}

	if m.clusterUsesParameterGroup(name) {
		return nil, cerrors.Newf(cerrors.FailedPrecondition, "parameter group %q is in use by a cluster", name)
	}

	m.parameterGroups.Delete(name)
	delete(m.paramOverrides, name)

	out := cloneParameterGroup(&pg)

	return &out, nil
}

// DescribeParameters returns the effective parameters of a group (catalog
// defaults with any overrides applied).
func (m *Mock) DescribeParameters(_ context.Context, groupName string) ([]mdbdriver.Parameter, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.parameterGroups.Has(groupName) {
		return nil, cerrors.Newf(cerrors.NotFound, "parameter group %q not found", groupName)
	}

	names := make([]string, 0, len(defaultParameters))
	for n := range defaultParameters {
		names = append(names, n)
	}

	sort.Strings(names)

	out := make([]mdbdriver.Parameter, 0, len(names))

	for _, n := range names {
		p := defaultParameters[n]
		if ov, ok := m.paramOverrides[groupName][n]; ok {
			p.Value = ov
		}

		out = append(out, p)
	}

	return out, nil
}
