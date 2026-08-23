package rds

import (
	"context"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
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

// paramsToDriver renders a stored parameter map as sorted driver Parameters,
// tagging each as user-set (the only kind the emulator tracks) and preserving
// each parameter's apply method.
func paramsToDriver(params map[string]rdsdriver.Parameter) []rdsdriver.Parameter {
	names := make([]string, 0, len(params))
	for name := range params {
		names = append(names, name)
	}

	sort.Strings(names)

	out := make([]rdsdriver.Parameter, 0, len(names))

	for _, name := range names {
		p := params[name]
		out = append(out, rdsdriver.Parameter{
			Name:        name,
			Value:       p.Value,
			ApplyMethod: applyMethodOrDefault(p.ApplyMethod),
			Source:      "user",
			ApplyType:   "static",
			DataType:    "string",
		})
	}

	return out
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

func (m *Mock) CreateDBParameterGroup(_ context.Context, cfg rdsdriver.ParameterGroupConfig) (*rdsdriver.ParameterGroup, error) {
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
		ARN:         parameterGroupARN(m.opts.Region, m.opts.AccountID, cfg.Name),
		Parameters:  map[string]rdsdriver.Parameter{},
	}
	m.paramGroups.Set(cfg.Name, pg)
	m.setGroupTags(pg.ARN, copyTags(cfg.Tags))

	out := pg

	return &out, nil
}

func (m *Mock) DescribeDBParameterGroups(_ context.Context, names []string) ([]rdsdriver.ParameterGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(names) == 0 {
		all := m.paramGroups.SortedValues()
		for i := range all {
			all[i].Parameters = copyParams(all[i].Parameters)
		}

		return all, nil
	}

	out := make([]rdsdriver.ParameterGroup, 0, len(names))

	for _, name := range names {
		pg, ok := m.paramGroups.Get(name)
		if !ok {
			return nil, errParameterGroupNotFound(name)
		}

		pg.Parameters = copyParams(pg.Parameters)
		out = append(out, pg)
	}

	return out, nil
}

func (m *Mock) ModifyDBParameterGroup(_ context.Context, name string, params []rdsdriver.Parameter) (*rdsdriver.ParameterGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pg, ok := m.paramGroups.Get(name)
	if !ok {
		return nil, errParameterGroupNotFound(name)
	}

	pg.Parameters = withParams(pg.Parameters, params)
	m.paramGroups.Set(name, pg)

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

func (m *Mock) DescribeDBParameters(_ context.Context, name string) ([]rdsdriver.Parameter, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pg, ok := m.paramGroups.Get(name)
	if !ok {
		return nil, errParameterGroupNotFound(name)
	}

	return paramsToDriver(pg.Parameters), nil
}

//nolint:dupl // structurally mirrors its sibling per-resource block by design.
func (m *Mock) ResetDBParameterGroup(_ context.Context, name string, params []string, resetAll bool) (*rdsdriver.ParameterGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pg, ok := m.paramGroups.Get(name)
	if !ok {
		return nil, errParameterGroupNotFound(name)
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
		ARN:         parameterGroupARN(m.opts.Region, m.opts.AccountID, target),
		Parameters:  copyParams(src.Parameters),
	}
	m.paramGroups.Set(target, pg)

	out := pg

	return &out, nil
}

// ---- DB cluster parameter groups ----

func (m *Mock) CreateDBClusterParameterGroup(
	_ context.Context, cfg rdsdriver.ParameterGroupConfig,
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
		ARN:         clusterParameterGroupARN(m.opts.Region, m.opts.AccountID, cfg.Name),
		Parameters:  map[string]rdsdriver.Parameter{},
	}
	m.clusterParamGroups.Set(cfg.Name, pg)
	m.setGroupTags(pg.ARN, copyTags(cfg.Tags))

	out := pg

	return &out, nil
}

func (m *Mock) DescribeDBClusterParameterGroups(_ context.Context, names []string) ([]rdsdriver.ClusterParameterGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(names) == 0 {
		all := m.clusterParamGroups.SortedValues()
		for i := range all {
			all[i].Parameters = copyParams(all[i].Parameters)
		}

		return all, nil
	}

	out := make([]rdsdriver.ClusterParameterGroup, 0, len(names))

	for _, name := range names {
		pg, ok := m.clusterParamGroups.Get(name)
		if !ok {
			return nil, errClusterParameterGroupNotFound(name)
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
		return nil, errClusterParameterGroupNotFound(name)
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

func (m *Mock) DescribeDBClusterParameters(_ context.Context, name string) ([]rdsdriver.Parameter, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pg, ok := m.clusterParamGroups.Get(name)
	if !ok {
		return nil, errClusterParameterGroupNotFound(name)
	}

	return paramsToDriver(pg.Parameters), nil
}

//nolint:dupl // structurally mirrors its sibling per-resource block by design.
func (m *Mock) ResetDBClusterParameterGroup(
	_ context.Context, name string, params []string, resetAll bool,
) (*rdsdriver.ClusterParameterGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pg, ok := m.clusterParamGroups.Get(name)
	if !ok {
		return nil, errClusterParameterGroupNotFound(name)
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
		ARN:         clusterParameterGroupARN(m.opts.Region, m.opts.AccountID, target),
		Parameters:  copyParams(src.Parameters),
	}
	m.clusterParamGroups.Set(target, pg)

	out := pg

	return &out, nil
}
