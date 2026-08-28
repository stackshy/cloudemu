package rds

import (
	"context"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/regionctx"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

var _ rdsdriver.OptionGroups = (*Mock)(nil)

// cloneOptions deep-copies each option, including its Settings map, so a
// returned option group never aliases the store (cloneSlice alone is shallow).
func cloneOptions(opts []rdsdriver.Option) []rdsdriver.Option {
	out := cloneSlice(opts)
	for i := range out {
		out[i].Settings = copyTags(out[i].Settings)
	}

	return out
}

// optionGroupInUseBy names an instance still attached to the given option
// group, if any.
func (m *Mock) optionGroupInUseBy(name string) (string, bool) {
	instances := m.instances.All()
	for id := range instances {
		if instances[id].OptionGroupName == name {
			return id, true
		}
	}

	return "", false
}

func optionGroupARN(region, accountID, name string) string {
	return idgen.AWSARN("rds", region, accountID, "og:"+name)
}

func errOptionGroupNotFound(name string) error {
	return cerrors.Newf(cerrors.NotFound, "option group %q not found", name)
}

// optionGroupOptionCatalog is a small, representative subset of the options
// each engine offers. Real AWS returns an exhaustive, version-specific catalog;
// the emulator returns only well-known, stable option names so a client that
// calls DescribeOptionGroupOptions gets a plausible non-empty answer.
//
//nolint:gochecknoglobals // static lookup table
var optionGroupOptionCatalog = map[string][]string{
	"mysql":        {"MARIADB_AUDIT_PLUGIN"},
	"mariadb":      {"MARIADB_AUDIT_PLUGIN"},
	"oracle-ee":    {"APEX", "OEM", "S3_INTEGRATION", "NATIVE_NETWORK_ENCRYPTION"},
	"oracle-se2":   {"APEX", "OEM", "S3_INTEGRATION"},
	"sqlserver-ee": {"TDE", "SQLSERVER_AUDIT"},
	"sqlserver-se": {"TDE", "SQLSERVER_AUDIT"},
}

func (m *Mock) CreateOptionGroup(ctx context.Context, cfg rdsdriver.OptionGroupConfig) (*rdsdriver.OptionGroup, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "OptionGroupName is required")
	}

	if cfg.EngineName == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "EngineName is required")
	}

	if strings.HasPrefix(cfg.Name, "default:") {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "option group name %q uses the reserved default: prefix", cfg.Name)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.optionGroups.Has(cfg.Name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "option group %q already exists", cfg.Name)
	}

	og := rdsdriver.OptionGroup{
		Name:               cfg.Name,
		EngineName:         cfg.EngineName,
		MajorEngineVersion: cfg.MajorEngineVersion,
		Description:        cfg.Description,
		ARN:                optionGroupARN(regionctx.RegionOr(ctx, m.opts.Region), m.opts.AccountID, cfg.Name),
	}
	m.optionGroups.Set(cfg.Name, og)
	m.setGroupTags(og.ARN, copyTags(cfg.Tags))

	out := og

	return &out, nil
}

func (m *Mock) DescribeOptionGroups(_ context.Context, names []string, engineName string) ([]rdsdriver.OptionGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(names) == 0 {
		all := m.optionGroups.SortedValues()

		filtered := make([]rdsdriver.OptionGroup, 0, len(all))

		for _, og := range all {
			if engineName != "" && og.EngineName != engineName {
				continue
			}

			og.Options = cloneOptions(og.Options)
			filtered = append(filtered, og)
		}

		return filtered, nil
	}

	out := make([]rdsdriver.OptionGroup, 0, len(names))

	for _, name := range names {
		og, ok := m.optionGroups.Get(name)
		if !ok {
			return nil, errOptionGroupNotFound(name)
		}

		og.Options = cloneSlice(og.Options)
		out = append(out, og)
	}

	return out, nil
}

func (m *Mock) ModifyOptionGroup(
	_ context.Context, name string, include []rdsdriver.Option, remove []string,
) (*rdsdriver.OptionGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	og, ok := m.optionGroups.Get(name)
	if !ok {
		return nil, errOptionGroupNotFound(name)
	}

	byName := make(map[string]rdsdriver.Option, len(og.Options))
	for _, o := range og.Options {
		byName[o.Name] = o
	}

	for _, o := range include {
		byName[o.Name] = o
	}

	for _, r := range remove {
		delete(byName, r)
	}

	// Fresh slice, not an in-place []:0 rebuild: a slice returned by a prior
	// DescribeOptionGroups must not be clobbered underneath its caller.
	opts := make([]rdsdriver.Option, 0, len(byName))
	for _, o := range byName {
		opts = append(opts, o)
	}

	sortOptions(opts)
	og.Options = opts
	m.optionGroups.Set(name, og)

	out := og

	return &out, nil
}

func (m *Mock) DeleteOptionGroup(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// AWS names default option groups "default:<engine>-<major>-<minor>".
	if strings.HasPrefix(name, "default:") {
		return cerrors.Newf(cerrors.FailedPrecondition, "option group %q is a default group and cannot be deleted", name)
	}

	if user, ok := m.optionGroupInUseBy(name); ok {
		return cerrors.Newf(cerrors.FailedPrecondition, "option group %q is in use by DB instance %q", name, user)
	}

	if !m.optionGroups.Delete(name) {
		return errOptionGroupNotFound(name)
	}

	return nil
}

func (m *Mock) CopyOptionGroup(_ context.Context, source, target, description string) (*rdsdriver.OptionGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	src, ok := m.optionGroups.Get(source)
	if !ok {
		return nil, errOptionGroupNotFound(source)
	}

	if m.optionGroups.Has(target) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "option group %q already exists", target)
	}

	if description == "" {
		description = src.Description
	}

	og := rdsdriver.OptionGroup{
		Name:               target,
		EngineName:         src.EngineName,
		MajorEngineVersion: src.MajorEngineVersion,
		Description:        description,
		ARN:                optionGroupARN(arnRegion(src.ARN, m.opts.Region), m.opts.AccountID, target),
		Options:            append([]rdsdriver.Option(nil), src.Options...),
	}
	m.optionGroups.Set(target, og)

	out := og

	return &out, nil
}

func (*Mock) DescribeOptionGroupOptions(
	_ context.Context, engineName, majorEngineVersion string,
) ([]rdsdriver.OptionGroupOption, error) {
	if engineName == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "EngineName is required")
	}

	names := optionGroupOptionCatalog[engineName]

	out := make([]rdsdriver.OptionGroupOption, 0, len(names))
	for _, n := range names {
		out = append(out, rdsdriver.OptionGroupOption{
			Name:               n,
			Description:        n + " option for " + engineName,
			EngineName:         engineName,
			MajorEngineVersion: majorEngineVersion,
		})
	}

	return out, nil
}

// sortOptions orders options by name for deterministic output.
func sortOptions(opts []rdsdriver.Option) {
	sort.Slice(opts, func(i, j int) bool { return opts[i].Name < opts[j].Name })
}
