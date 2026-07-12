// Package ssm provides an in-memory mock implementation of AWS Systems Manager
// (SSM) Parameter Store.
//
// This is the Layer-3 driver implementation. The portable Layer-1 wrapper that
// adds recording/metrics/rate-limiting/error-injection/latency lives in the
// module-root parameterstore package (parameterstore/parameterstore.go).
//
// Values are stored verbatim regardless of Type; SecureString parameters are
// NOT encrypted (there is no real KMS integration), so WithDecryption is a
// no-op and the raw value is always returned.
package ssm

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
	"github.com/stackshy/cloudemu/parameterstore/driver"
)

// Compile-time check that Mock implements driver.ParameterStore.
var _ driver.ParameterStore = (*Mock)(nil)

// version is a single stored revision of a parameter.
type version struct {
	value        string
	typ          string
	dataType     string
	version      int64
	lastModified string
	labels       []string
}

// paramData holds all versions and current metadata for a parameter name.
type paramData struct {
	name        string
	description string
	tier        string
	versions    []*version
	latest      int64
	mu          sync.RWMutex
}

// Mock is an in-memory mock implementation of SSM Parameter Store.
type Mock struct {
	params *memstore.Store[*paramData]
	opts   *config.Options
}

// New creates a new SSM Parameter Store mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		params: memstore.New[*paramData](),
		opts:   opts,
	}
}

func (m *Mock) now() string {
	return m.opts.Clock.Now().UTC().Format(time.RFC3339)
}

// arn builds the ARN for a parameter. AWS uses "parameter" + the name (which
// begins with "/" for hierarchical names), joined without a separating slash.
func (m *Mock) arn(name string) string {
	return idgen.AWSARN("ssm", m.opts.Region, m.opts.AccountID, "parameter"+ensureLeadingSlash(name))
}

// ensureLeadingSlash normalizes a name so hierarchical ARNs render like
// "parameter/a/b" while flat names render like "parameter/flat".
func ensureLeadingSlash(name string) string {
	if strings.HasPrefix(name, "/") {
		return name
	}

	return "/" + name
}

func defaultType(t string) string {
	switch t {
	case driver.TypeString, driver.TypeStringList, driver.TypeSecureString:
		return t
	default:
		return driver.TypeString
	}
}

// PutParameter creates a new parameter or, when Overwrite is set, appends a new
// version to an existing one.
func (m *Mock) PutParameter(_ context.Context, cfg driver.PutConfig) (int64, string, error) {
	if cfg.Name == "" {
		return 0, "", errors.New(errors.InvalidArgument, "parameter name is required")
	}

	tier := cfg.Tier
	if tier == "" {
		tier = "Standard"
	}

	dataType := cfg.DataType
	if dataType == "" {
		dataType = "text"
	}

	now := m.now()

	if existing, ok := m.params.Get(cfg.Name); ok {
		existing.mu.Lock()
		defer existing.mu.Unlock()

		if !cfg.Overwrite {
			return 0, "", errors.Newf(errors.AlreadyExists,
				"parameter %q already exists; set Overwrite to update it", cfg.Name)
		}

		next := existing.latest + 1
		existing.versions = append(existing.versions, &version{
			value:        cfg.Value,
			typ:          defaultType(cfg.Type),
			dataType:     dataType,
			version:      next,
			lastModified: now,
		})
		existing.latest = next
		existing.description = cfg.Description
		existing.tier = tier

		return next, tier, nil
	}

	pd := &paramData{
		name:        cfg.Name,
		description: cfg.Description,
		tier:        tier,
		latest:      1,
		versions: []*version{{
			value:        cfg.Value,
			typ:          defaultType(cfg.Type),
			dataType:     dataType,
			version:      1,
			lastModified: now,
		}},
	}

	// SetIfAbsent guards against a concurrent create racing between Get and Set.
	if !m.params.SetIfAbsent(cfg.Name, pd) {
		// Lost the race: retry as an overwrite path only if allowed.
		if !cfg.Overwrite {
			return 0, "", errors.Newf(errors.AlreadyExists,
				"parameter %q already exists; set Overwrite to update it", cfg.Name)
		}

		cfg.Overwrite = true

		return m.PutParameter(context.Background(), cfg)
	}

	return 1, tier, nil
}

// resolveSelector splits a name of the form "name:selector" into its base name
// and selector (a version number or a label). An empty selector means latest.
func resolveSelector(name string) (base, selector string) {
	// Hierarchical names contain slashes but never a colon in the path itself,
	// so the last colon (if any) introduces a version/label selector.
	if i := strings.LastIndex(name, ":"); i >= 0 {
		return name[:i], name[i+1:]
	}

	return name, ""
}

// pick returns the version matching the selector (empty = latest, numeric =
// version, otherwise a label). Callers must hold pd.mu.
func (pd *paramData) pick(selector string) (*version, bool) {
	if selector == "" {
		return pd.versionByNumber(pd.latest)
	}

	if n, err := strconv.ParseInt(selector, 10, 64); err == nil {
		return pd.versionByNumber(n)
	}

	for _, v := range pd.versions {
		for _, l := range v.labels {
			if l == selector {
				return v, true
			}
		}
	}

	return nil, false
}

func (pd *paramData) versionByNumber(n int64) (*version, bool) {
	for _, v := range pd.versions {
		if v.version == n {
			return v, true
		}
	}

	return nil, false
}

func (m *Mock) toParameter(pd *paramData, v *version, selector string) driver.Parameter {
	name := pd.name
	if selector != "" {
		name = pd.name + ":" + selector
	}

	return driver.Parameter{
		Name:         pd.name,
		Type:         v.typ,
		Value:        v.value,
		Version:      v.version,
		ARN:          m.arn(pd.name),
		DataType:     v.dataType,
		LastModified: v.lastModified,
		Selector:     selectorFor(name, pd.name),
	}
}

func selectorFor(requested, base string) string {
	if requested == base {
		return ""
	}

	return strings.TrimPrefix(requested, base+":")
}

// GetParameter retrieves a single parameter by name, honoring an optional
// ":version" or ":label" selector suffix. withDecryption is accepted but has no
// effect: SecureString values are stored and returned in the clear.
func (m *Mock) GetParameter(_ context.Context, name string, _ bool) (*driver.Parameter, error) {
	base, selector := resolveSelector(name)

	pd, ok := m.params.Get(base)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "parameter %q not found", base)
	}

	pd.mu.RLock()
	defer pd.mu.RUnlock()

	v, ok := pd.pick(selector)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "parameter %q version/label %q not found", base, selector)
	}

	p := m.toParameter(pd, v, selector)

	return &p, nil
}

// GetParameters retrieves multiple parameters, reporting names that were not
// found (or whose selector did not resolve) as invalid rather than erroring.
func (m *Mock) GetParameters(_ context.Context, names []string, _ bool) ([]driver.Parameter, []string, error) {
	found := make([]driver.Parameter, 0, len(names))

	var invalid []string

	for _, name := range names {
		base, selector := resolveSelector(name)

		pd, ok := m.params.Get(base)
		if !ok {
			invalid = append(invalid, name)
			continue
		}

		pd.mu.RLock()
		v, ok := pd.pick(selector)
		if !ok {
			pd.mu.RUnlock()

			invalid = append(invalid, name)

			continue
		}

		found = append(found, m.toParameter(pd, v, selector))
		pd.mu.RUnlock()
	}

	return found, invalid, nil
}

// GetParametersByPath returns the latest version of every parameter under a
// hierarchical path. With Recursive false, only direct children are returned;
// with Recursive true, the whole subtree is returned.
func (m *Mock) GetParametersByPath(_ context.Context, in driver.GetByPathInput) ([]driver.Parameter, error) {
	path := in.Path
	if path == "" {
		path = "/"
	}

	if !strings.HasPrefix(path, "/") {
		return nil, errors.New(errors.InvalidArgument, "path must begin with '/'")
	}

	prefix := path
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	var out []driver.Parameter

	for _, pd := range m.params.All() {
		name := pd.name
		if !strings.HasPrefix(name, prefix) {
			continue
		}

		rest := strings.TrimPrefix(name, prefix)
		// Non-recursive: only direct children (no further "/" in the remainder).
		if !in.Recursive && strings.Contains(rest, "/") {
			continue
		}

		pd.mu.RLock()
		if v, ok := pd.versionByNumber(pd.latest); ok {
			out = append(out, m.toParameter(pd, v, ""))
		}
		pd.mu.RUnlock()
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

// DeleteParameter removes a parameter and all its versions.
func (m *Mock) DeleteParameter(_ context.Context, name string) error {
	if !m.params.Delete(name) {
		return errors.Newf(errors.NotFound, "parameter %q not found", name)
	}

	return nil
}

// DeleteParameters removes multiple parameters, returning the names deleted and
// the names that did not exist.
func (m *Mock) DeleteParameters(_ context.Context, names []string) ([]string, []string, error) {
	var deleted, invalid []string

	for _, name := range names {
		if m.params.Delete(name) {
			deleted = append(deleted, name)
		} else {
			invalid = append(invalid, name)
		}
	}

	return deleted, invalid, nil
}

// DescribeParameters lists metadata (no values) for all parameters.
func (m *Mock) DescribeParameters(_ context.Context) ([]driver.ParameterMetadata, error) {
	all := m.params.All()

	out := make([]driver.ParameterMetadata, 0, len(all))

	for _, pd := range all {
		pd.mu.RLock()
		if v, ok := pd.versionByNumber(pd.latest); ok {
			out = append(out, driver.ParameterMetadata{
				Name:             pd.name,
				Type:             v.typ,
				Description:      pd.description,
				Version:          pd.latest,
				ARN:              m.arn(pd.name),
				Tier:             pd.tier,
				DataType:         v.dataType,
				LastModified:     v.lastModified,
				LastModifiedUser: idgen.AWSARN("iam", "", m.opts.AccountID, "user/cloudemu"),
			})
		}
		pd.mu.RUnlock()
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

// GetParameterHistory returns every version of a parameter, oldest first.
func (m *Mock) GetParameterHistory(_ context.Context, name string) ([]driver.Parameter, error) {
	pd, ok := m.params.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "parameter %q not found", name)
	}

	pd.mu.RLock()
	defer pd.mu.RUnlock()

	out := make([]driver.Parameter, 0, len(pd.versions))
	for _, v := range pd.versions {
		out = append(out, driver.Parameter{
			Name:         pd.name,
			Type:         v.typ,
			Value:        v.value,
			Version:      v.version,
			ARN:          m.arn(pd.name),
			DataType:     v.dataType,
			LastModified: v.lastModified,
		})
	}

	return out, nil
}

// LabelParameterVersion attaches labels to a specific version (0 = latest),
// returning the version the labels were applied to and any labels rejected.
// A label attached to a new version is removed from any older version that held
// it, matching real SSM semantics.
func (m *Mock) LabelParameterVersion(_ context.Context, name string, ver int64, labels []string) (int64, []string, error) {
	pd, ok := m.params.Get(name)
	if !ok {
		return 0, nil, errors.Newf(errors.NotFound, "parameter %q not found", name)
	}

	pd.mu.Lock()
	defer pd.mu.Unlock()

	if ver == 0 {
		ver = pd.latest
	}

	target, ok := pd.versionByNumber(ver)
	if !ok {
		return 0, nil, errors.Newf(errors.NotFound, "parameter %q version %d not found", name, ver)
	}

	for _, label := range labels {
		// Detach the label from any other version first.
		for _, v := range pd.versions {
			if v == target {
				continue
			}

			v.labels = removeString(v.labels, label)
		}

		if !containsString(target.labels, label) {
			target.labels = append(target.labels, label)
		}
	}

	return ver, nil, nil
}

func containsString(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}

	return false
}

func removeString(ss []string, s string) []string {
	out := ss[:0]

	for _, x := range ss {
		if x != s {
			out = append(out, x)
		}
	}

	return out
}
