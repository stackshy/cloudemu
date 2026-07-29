package rds

import (
	"context"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

var _ rdsdriver.ParameterGroups = (*Mock)(nil)

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

// paramsToDriver renders a stored name->value map as sorted driver Parameters,
// tagging each as user-set (the only kind the emulator tracks).
func paramsToDriver(params map[string]string) []rdsdriver.Parameter {
	names := make([]string, 0, len(params))
	for name := range params {
		names = append(names, name)
	}

	sort.Strings(names)

	out := make([]rdsdriver.Parameter, 0, len(names))
	for _, name := range names {
		out = append(out, rdsdriver.Parameter{
			Name:        name,
			Value:       params[name],
			ApplyMethod: "pending-reboot",
			Source:      "user",
			ApplyType:   "static",
			DataType:    "string",
		})
	}

	return out
}

// mergeParams applies the given parameters onto a group's value map.
func mergeParams(dst map[string]string, params []rdsdriver.Parameter) {
	for _, p := range params {
		dst[p.Name] = p.Value
	}
}

// ---- DB parameter groups ----

func (m *Mock) CreateDBParameterGroup(_ context.Context, cfg rdsdriver.ParameterGroupConfig) (*rdsdriver.ParameterGroup, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "DBParameterGroupName is required")
	}

	if cfg.Family == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "DBParameterGroupFamily is required")
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
		Parameters:  map[string]string{},
	}
	m.paramGroups.Set(cfg.Name, pg)

	out := pg

	return &out, nil
}

//nolint:dupl // structurally mirrors its sibling per-resource block by design.
func (m *Mock) DescribeDBParameterGroups(_ context.Context, names []string) ([]rdsdriver.ParameterGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(names) == 0 {
		return m.paramGroups.SortedValues(), nil
	}

	out := make([]rdsdriver.ParameterGroup, 0, len(names))

	for _, name := range names {
		pg, ok := m.paramGroups.Get(name)
		if !ok {
			return nil, errParameterGroupNotFound(name)
		}

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

	mergeParams(pg.Parameters, params)
	m.paramGroups.Set(name, pg)

	out := pg

	return &out, nil
}

func (m *Mock) DeleteDBParameterGroup(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

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
		pg.Parameters = map[string]string{}
	} else {
		for _, p := range params {
			delete(pg.Parameters, p)
		}
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
		Parameters:  copyTags(src.Parameters),
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
		Parameters:  map[string]string{},
	}
	m.clusterParamGroups.Set(cfg.Name, pg)

	out := pg

	return &out, nil
}

//nolint:dupl // structurally mirrors its sibling per-resource block by design.
func (m *Mock) DescribeDBClusterParameterGroups(_ context.Context, names []string) ([]rdsdriver.ClusterParameterGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(names) == 0 {
		return m.clusterParamGroups.SortedValues(), nil
	}

	out := make([]rdsdriver.ClusterParameterGroup, 0, len(names))

	for _, name := range names {
		pg, ok := m.clusterParamGroups.Get(name)
		if !ok {
			return nil, errClusterParameterGroupNotFound(name)
		}

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

	mergeParams(pg.Parameters, params)
	m.clusterParamGroups.Set(name, pg)

	out := pg

	return &out, nil
}

func (m *Mock) DeleteDBClusterParameterGroup(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

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
		pg.Parameters = map[string]string{}
	} else {
		for _, p := range params {
			delete(pg.Parameters, p)
		}
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
		Parameters:  copyTags(src.Parameters),
	}
	m.clusterParamGroups.Set(target, pg)

	out := pg

	return &out, nil
}
