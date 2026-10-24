package rds

import (
	"context"
	"maps"
	"slices"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/regionctx"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

var _ rdsdriver.ParameterGroups = (*Mock)(nil)

// dbParameterGroupInUseBy names an instance still attached to the given DB
// parameter group, if any.
func (m *Mock) dbParameterGroupInUseBy(name string) (string, bool) {
	instances := m.instances.All()
	for id := range instances {
		if instances[id].DBParameterGroupName == name {
			return id, true
		}
	}

	return "", false
}

// clusterParameterGroupInUseBy names a cluster still attached to the given DB
// cluster parameter group, if any.
func (m *Mock) clusterParameterGroupInUseBy(name string) (string, bool) {
	clusters := m.clusters.All()
	for id := range clusters {
		if clusters[id].DBClusterParameterGroupName == name {
			return id, true
		}
	}

	return "", false
}

func parameterGroupARN(region, accountID, name string) string {
	return idgen.AWSARN("rds", region, accountID, "pg:"+name)
}

func clusterParameterGroupARN(region, accountID, name string) string {
	return idgen.AWSARN("rds", region, accountID, "cluster-pg:"+name)
}

func errParameterGroupNotFound(name string) error {
	return cerrors.Newf(cerrors.NotFound, "DB parameter group %q not found", name)
}

func errClusterParameterGroupNotFound(name string) error {
	return cerrors.Newf(cerrors.NotFound, "DB cluster parameter group %q not found", name)
}

// missingGroupErr is the error for a modify/reset of a DB or cluster parameter
// group that is not stored. A "default.<family>" group always exists in RDS (and
// DescribeDB[Cluster]ParameterGroups lists it), but its parameters are fixed:
// real RDS answers "Cannot modify a default parameter group." with
// InvalidParameterValue rather than denying the group exists. Any other name
// is genuinely unknown and gets notFound.
func missingGroupErr(name string, notFound error) error {
	if _, isDefault := defaultGroupFamily(name); isDefault {
		return cerrors.New(cerrors.InvalidArgument, "Cannot modify a default parameter group.")
	}

	return notFound
}

func applyMethodOrDefault(m string) string {
	if m == "" {
		return "pending-reboot"
	}

	return m
}

// withParams returns a NEW map: a copy of existing with params applied. Never
// mutates the input, so a map handed out by a Describe call is never written
// to concurrently (copy-on-read + replace-on-write).
func withParams(existing map[string]rdsdriver.Parameter, params []rdsdriver.Parameter) map[string]rdsdriver.Parameter {
	out := copyParams(existing)
	for _, p := range params {
		out[p.Name] = rdsdriver.Parameter{Name: p.Name, Value: p.Value, ApplyMethod: p.ApplyMethod}
	}

	return out
}

func copyParams(src map[string]rdsdriver.Parameter) map[string]rdsdriver.Parameter {
	out := make(map[string]rdsdriver.Parameter, len(src))
	for k, v := range src {
		out[k] = v
	}

	return out
}

// ---- DB parameter groups ----

func (m *Mock) CreateDBParameterGroup(ctx context.Context, cfg rdsdriver.ParameterGroupConfig) (*rdsdriver.ParameterGroup, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "DBParameterGroupName is required")
	}

	if cfg.Family == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "DBParameterGroupFamily is required")
	}

	if strings.HasPrefix(cfg.Name, "default.") {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "DB parameter group name %q uses the reserved default. prefix", cfg.Name)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.paramGroups.Has(cfg.Name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "DB parameter group %q already exists", cfg.Name)
	}

	pg := rdsdriver.ParameterGroup{
		Name:        cfg.Name,
		Family:      cfg.Family,
		Description: cfg.Description,
		ARN:         parameterGroupARN(regionctx.RegionOr(ctx, m.opts.Region), m.opts.AccountID, cfg.Name),
		Parameters:  map[string]rdsdriver.Parameter{},
	}
	m.paramGroups.Set(cfg.Name, pg)
	m.setGroupTags(pg.ARN, copyTags(cfg.Tags))

	out := pg

	return &out, nil
}

// DescribeDBParameterGroups lists the stored groups plus the always-present
// "default.<family>" groups RDS associates with instances created without one.
// A default group is synthesized (never stored, so it cannot be modified or
// deleted) when an instance references it, or when it is named explicitly,
// mirroring DescribeDBParameters.
func (m *Mock) DescribeDBParameterGroups(ctx context.Context, names []string) ([]rdsdriver.ParameterGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	region := regionctx.RegionOr(ctx, m.opts.Region)

	if len(names) == 0 {
		all := m.paramGroups.SortedValues()
		for i := range all {
			all[i].Parameters = copyParams(all[i].Parameters)
		}

		for _, name := range m.referencedDefaultParamGroups() {
			family, _ := defaultGroupFamily(name)
			all = append(all, m.defaultParameterGroup(region, name, family))
		}

		slices.SortFunc(all, func(a, b rdsdriver.ParameterGroup) int { return strings.Compare(a.Name, b.Name) })

		return all, nil
	}

	out := make([]rdsdriver.ParameterGroup, 0, len(names))

	for _, name := range names {
		pg, ok := m.paramGroups.Get(name)
		if !ok {
			family, isDefault := defaultGroupFamily(name)
			if !isDefault {
				return nil, errParameterGroupNotFound(name)
			}

			pg = m.defaultParameterGroup(region, name, family)
		}

		pg.Parameters = copyParams(pg.Parameters)
		out = append(out, pg)
	}

	return out, nil
}

func (m *Mock) ModifyDBParameterGroup(_ context.Context, name string, params []rdsdriver.Parameter) (*rdsdriver.ParameterGroup, error) {
	defer m.flushEventNotifications() // runs after the unlock below

	m.mu.Lock()
	defer m.mu.Unlock()

	pg, ok := m.paramGroups.Get(name)
	if !ok {
		return nil, missingGroupErr(name, errParameterGroupNotFound(name))
	}

	pg.Parameters = withParams(pg.Parameters, params)
	m.paramGroups.Set(name, pg)

	// Real RDS raises one RDS-EVENT-0037 per updated parameter.
	for _, prm := range params {
		m.recordEventLocked(eventSource{sourceDBParameterGroup, name, pg.ARN}, evParameterUpdated,
			prm.Name, prm.Value, applyMethodOrDefault(prm.ApplyMethod))
	}

	out := pg

	return &out, nil
}

func (m *Mock) DeleteDBParameterGroup(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if strings.HasPrefix(name, "default.") {
		return cerrors.Newf(cerrors.FailedPrecondition, "DB parameter group %q is a default group and cannot be deleted", name)
	}

	if user, ok := m.dbParameterGroupInUseBy(name); ok {
		return cerrors.Newf(cerrors.FailedPrecondition, "DB parameter group %q is in use by DB instance %q", name, user)
	}

	if !m.paramGroups.Delete(name) {
		return errParameterGroupNotFound(name)
	}

	return nil
}

// DescribeDBParameters returns the group's full parameter set: the engine
// defaults for its family overlaid with any user modifications, matching real
// RDS (which never returns an empty list). A "default.<family>" group name that
// was never explicitly created is synthesized from the family in its name, so
// the always-present default parameter groups resolve too.
func (m *Mock) DescribeDBParameters(_ context.Context, name string) ([]rdsdriver.Parameter, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pg, ok := m.paramGroups.Get(name)
	if !ok {
		if family, isDefault := defaultGroupFamily(name); isDefault {
			return mergedParameters(family, nil), nil
		}

		return nil, errParameterGroupNotFound(name)
	}

	return mergedParameters(pg.Family, pg.Parameters), nil
}

// defaultGroupFamily extracts the family from a "default.<family>" group name
// (e.g. "default.mysql8.0" -> "mysql8.0"). RDS provides one such always-present
// group per engine family, so only a known family counts: "default.bogus" is
// not a group and stays DBParameterGroupNotFound.
func defaultGroupFamily(name string) (string, bool) {
	family, ok := strings.CutPrefix(name, "default.")
	if !ok || !isKnownFamily(family) {
		return "", false
	}

	return family, true
}

// dbParameterGroupFamily returns the family of the DB parameter group named
// name: a stored group's family, or the family of a "default.<family>" group.
// It reports false for a name that is neither. The caller holds m.mu.
func (m *Mock) dbParameterGroupFamily(name string) (string, bool) {
	if pg, ok := m.paramGroups.Get(name); ok {
		return pg.Family, pg.Family != ""
	}

	return defaultGroupFamily(name)
}

//nolint:dupl // structurally mirrors its sibling per-resource block by design.
func (m *Mock) ResetDBParameterGroup(_ context.Context, name string, params []string, resetAll bool) (*rdsdriver.ParameterGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pg, ok := m.paramGroups.Get(name)
	if !ok {
		return nil, missingGroupErr(name, errParameterGroupNotFound(name))
	}

	if resetAll {
		pg.Parameters = map[string]rdsdriver.Parameter{}
	} else {
		next := copyParams(pg.Parameters)
		for _, p := range params {
			delete(next, p)
		}

		pg.Parameters = next
	}

	m.paramGroups.Set(name, pg)

	out := pg

	return &out, nil
}

func (m *Mock) CopyDBParameterGroup(_ context.Context, source, target, description string) (*rdsdriver.ParameterGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	src, ok := m.paramGroups.Get(source)
	if !ok {
		return nil, errParameterGroupNotFound(source)
	}

	if m.paramGroups.Has(target) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "DB parameter group %q already exists", target)
	}

	if description == "" {
		description = src.Description
	}

	pg := rdsdriver.ParameterGroup{
		Name:        target,
		Family:      src.Family,
		Description: description,
		ARN:         parameterGroupARN(arnRegion(src.ARN, m.opts.Region), m.opts.AccountID, target),
		Parameters:  copyParams(src.Parameters),
	}
	m.paramGroups.Set(target, pg)

	out := pg

	return &out, nil
}

// ---- DB cluster parameter groups ----

func (m *Mock) CreateDBClusterParameterGroup(
	ctx context.Context, cfg rdsdriver.ParameterGroupConfig,
) (*rdsdriver.ClusterParameterGroup, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "DBClusterParameterGroupName is required")
	}

	if cfg.Family == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "DBParameterGroupFamily is required")
	}

	if strings.HasPrefix(cfg.Name, "default.") {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "DB cluster parameter group name %q uses the reserved default. prefix", cfg.Name)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.clusterParamGroups.Has(cfg.Name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "DB cluster parameter group %q already exists", cfg.Name)
	}

	pg := rdsdriver.ClusterParameterGroup{
		Name:        cfg.Name,
		Family:      cfg.Family,
		Description: cfg.Description,
		ARN:         clusterParameterGroupARN(regionctx.RegionOr(ctx, m.opts.Region), m.opts.AccountID, cfg.Name),
		Parameters:  map[string]rdsdriver.Parameter{},
	}
	m.clusterParamGroups.Set(cfg.Name, pg)
	m.setGroupTags(pg.ARN, copyTags(cfg.Tags))

	out := pg

	return &out, nil
}

// DescribeDBClusterParameterGroups mirrors DescribeDBParameterGroups for
// cluster parameter groups, synthesizing the "default.<family>" groups that
// clusters reference or that are named explicitly.
func (m *Mock) DescribeDBClusterParameterGroups(
	ctx context.Context, names []string,
) ([]rdsdriver.ClusterParameterGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	region := regionctx.RegionOr(ctx, m.opts.Region)

	if len(names) == 0 {
		all := m.clusterParamGroups.SortedValues()
		for i := range all {
			all[i].Parameters = copyParams(all[i].Parameters)
		}

		for _, name := range m.referencedDefaultClusterParamGroups() {
			family, _ := defaultGroupFamily(name)
			all = append(all, m.defaultClusterParameterGroup(region, name, family))
		}

		slices.SortFunc(all, func(a, b rdsdriver.ClusterParameterGroup) int { return strings.Compare(a.Name, b.Name) })

		return all, nil
	}

	out := make([]rdsdriver.ClusterParameterGroup, 0, len(names))

	for _, name := range names {
		pg, ok := m.clusterParamGroups.Get(name)
		if !ok {
			family, isDefault := defaultGroupFamily(name)
			if !isDefault {
				return nil, errClusterParameterGroupNotFound(name)
			}

			pg = m.defaultClusterParameterGroup(region, name, family)
		}

		pg.Parameters = copyParams(pg.Parameters)
		out = append(out, pg)
	}

	return out, nil
}

func (m *Mock) ModifyDBClusterParameterGroup(
	_ context.Context, name string, params []rdsdriver.Parameter,
) (*rdsdriver.ClusterParameterGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pg, ok := m.clusterParamGroups.Get(name)
	if !ok {
		return nil, missingGroupErr(name, errClusterParameterGroupNotFound(name))
	}

	pg.Parameters = withParams(pg.Parameters, params)
	m.clusterParamGroups.Set(name, pg)

	out := pg

	return &out, nil
}

func (m *Mock) DeleteDBClusterParameterGroup(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if strings.HasPrefix(name, "default.") {
		return cerrors.Newf(cerrors.FailedPrecondition, "DB cluster parameter group %q is a default group and cannot be deleted", name)
	}

	if user, ok := m.clusterParameterGroupInUseBy(name); ok {
		return cerrors.Newf(cerrors.FailedPrecondition, "DB cluster parameter group %q is in use by DB cluster %q", name, user)
	}

	if !m.clusterParamGroups.Delete(name) {
		return errClusterParameterGroupNotFound(name)
	}

	return nil
}

// DescribeDBClusterParameters mirrors DescribeDBParameters for Aurora cluster
// parameter groups: the family's engine defaults overlaid with user changes,
// with "default.<family>" cluster groups synthesized from their name.
func (m *Mock) DescribeDBClusterParameters(_ context.Context, name string) ([]rdsdriver.Parameter, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pg, ok := m.clusterParamGroups.Get(name)
	if !ok {
		if family, isDefault := defaultGroupFamily(name); isDefault {
			return mergedParameters(family, nil), nil
		}

		return nil, errClusterParameterGroupNotFound(name)
	}

	return mergedParameters(pg.Family, pg.Parameters), nil
}

//nolint:dupl // structurally mirrors its sibling per-resource block by design.
func (m *Mock) ResetDBClusterParameterGroup(
	_ context.Context, name string, params []string, resetAll bool,
) (*rdsdriver.ClusterParameterGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pg, ok := m.clusterParamGroups.Get(name)
	if !ok {
		return nil, missingGroupErr(name, errClusterParameterGroupNotFound(name))
	}

	if resetAll {
		pg.Parameters = map[string]rdsdriver.Parameter{}
	} else {
		next := copyParams(pg.Parameters)
		for _, p := range params {
			delete(next, p)
		}

		pg.Parameters = next
	}

	m.clusterParamGroups.Set(name, pg)

	out := pg

	return &out, nil
}

func (m *Mock) CopyDBClusterParameterGroup(
	_ context.Context, source, target, description string,
) (*rdsdriver.ClusterParameterGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	src, ok := m.clusterParamGroups.Get(source)
	if !ok {
		return nil, errClusterParameterGroupNotFound(source)
	}

	if m.clusterParamGroups.Has(target) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "DB cluster parameter group %q already exists", target)
	}

	if description == "" {
		description = src.Description
	}

	pg := rdsdriver.ClusterParameterGroup{
		Name:        target,
		Family:      src.Family,
		Description: description,
		ARN:         clusterParameterGroupARN(arnRegion(src.ARN, m.opts.Region), m.opts.AccountID, target),
		Parameters:  copyParams(src.Parameters),
	}
	m.clusterParamGroups.Set(target, pg)

	out := pg

	return &out, nil
}

// defaultParameterGroup synthesizes the always-present "default.<family>" DB
// parameter group, described as RDS describes it ("Default parameter group for
// <family>").
func (m *Mock) defaultParameterGroup(region, name, family string) rdsdriver.ParameterGroup {
	return rdsdriver.ParameterGroup{
		Name:        name,
		Family:      family,
		Description: "Default parameter group for " + family,
		ARN:         parameterGroupARN(region, m.opts.AccountID, name),
		Parameters:  map[string]rdsdriver.Parameter{},
	}
}

// defaultClusterParameterGroup synthesizes the always-present
// "default.<family>" DB cluster parameter group.
func (m *Mock) defaultClusterParameterGroup(region, name, family string) rdsdriver.ClusterParameterGroup {
	return rdsdriver.ClusterParameterGroup{
		Name:        name,
		Family:      family,
		Description: "Default cluster parameter group for " + family,
		ARN:         clusterParameterGroupARN(region, m.opts.AccountID, name),
		Parameters:  map[string]rdsdriver.Parameter{},
	}
}

// referencedDefaultParamGroups returns, sorted and de-duplicated, the
// "default.<family>" DB parameter groups referenced by instances that are not
// stored as explicit groups. The caller holds m.mu.
func (m *Mock) referencedDefaultParamGroups() []string {
	seen := map[string]struct{}{}

	instances := m.instances.All()
	for id := range instances {
		name := instances[id].DBParameterGroupName
		if _, isDefault := defaultGroupFamily(name); isDefault && !m.paramGroups.Has(name) {
			seen[name] = struct{}{}
		}
	}

	return slices.Sorted(maps.Keys(seen))
}

// referencedDefaultClusterParamGroups is referencedDefaultParamGroups for the
// cluster parameter groups clusters reference. The caller holds m.mu.
func (m *Mock) referencedDefaultClusterParamGroups() []string {
	seen := map[string]struct{}{}

	clusters := m.clusters.All()
	for id := range clusters {
		name := clusters[id].DBClusterParameterGroupName
		if _, isDefault := defaultGroupFamily(name); isDefault && !m.clusterParamGroups.Has(name) {
			seen[name] = struct{}{}
		}
	}

	return slices.Sorted(maps.Keys(seen))
}
