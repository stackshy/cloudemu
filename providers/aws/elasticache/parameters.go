package elasticache

import (
	"context"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// Parameter is one entry in a cache parameter group's detailed parameter list,
// as returned by DescribeCacheParameters. Source is "system" for an
// engine-default value and "user" once the parameter has been modified via
// ModifyCacheParameterGroup.
type Parameter struct {
	Name                 string
	Value                string
	Source               string
	DataType             string
	AllowedValues        string
	IsModifiable         bool
	MinimumEngineVersion string
	Description          string
}

// ParameterUpdate is one name→value pair from a ModifyCacheParameterGroup /
// ResetCacheParameterGroup ParameterNameValues list. For a reset the Value is
// ignored — only the Name identifies the parameter to restore to its default.
type ParameterUpdate struct {
	Name  string
	Value string
}

const (
	paramSourceSystem = "system"
	paramSourceUser   = "user"

	paramSourceEngineDefault = "engine-default"
)

// defaultCacheParameters returns a curated set of engine-default parameters for
// a cache parameter group family (e.g. "redis7", "memcached1.6"). It is not the
// full real list of hundreds — it is the representative subset IaC reads and the
// parameters commonly set through Terraform (notably maxmemory-policy for
// Redis). Every entry carries Source "system".
func defaultCacheParameters(family string) []Parameter {
	if isMemcachedFamily(family) {
		return memcachedDefaultParameters()
	}

	return redisDefaultParameters()
}

// isMemcachedFamily reports whether a parameter-group family names the Memcached
// engine. Anything else (redis*, valkey*, or an unknown family) is treated as
// Redis-shaped, which is the ElastiCache default.
func isMemcachedFamily(family string) bool {
	f := strings.ToLower(strings.TrimPrefix(family, "default."))

	return strings.HasPrefix(f, "memcached")
}

const maxmemoryPolicyAllowed = "volatile-lru,allkeys-lru,volatile-lfu,allkeys-lfu," +
	"volatile-random,allkeys-random,volatile-ttl,noeviction"

func redisDefaultParameters() []Parameter {
	const minVer = "5.0.0"

	return []Parameter{
		{Name: "maxmemory-policy", Value: "volatile-lru", DataType: "string", AllowedValues: maxmemoryPolicyAllowed,
			IsModifiable: true, MinimumEngineVersion: minVer, Description: "Max memory policy."},
		{Name: "maxmemory-samples", Value: "3", DataType: "integer", AllowedValues: "1-",
			IsModifiable: true, MinimumEngineVersion: minVer, Description: "Max memory samples."},
		{Name: "timeout", Value: "0", DataType: "integer", AllowedValues: "0,20-",
			IsModifiable: true, MinimumEngineVersion: minVer, Description: "Close idle client connections after N seconds."},
		{Name: "tcp-keepalive", Value: "300", DataType: "integer", AllowedValues: "0-",
			IsModifiable: true, MinimumEngineVersion: minVer, Description: "TCP keepalive interval in seconds."},
		{Name: "slowlog-log-slower-than", Value: "10000", DataType: "integer", AllowedValues: "-1-",
			IsModifiable: true, MinimumEngineVersion: minVer, Description: "Slowlog threshold in microseconds."},
		{Name: "slowlog-max-len", Value: "128", DataType: "integer", AllowedValues: "0-",
			IsModifiable: true, MinimumEngineVersion: minVer, Description: "Maximum number of slowlog entries."},
		{Name: "notify-keyspace-events", Value: "", DataType: "string", AllowedValues: "",
			IsModifiable: true, MinimumEngineVersion: minVer, Description: "Keyspace notifications configuration."},
		{Name: "reserved-memory-percent", Value: "25", DataType: "integer", AllowedValues: "0-100",
			IsModifiable: true, MinimumEngineVersion: minVer, Description: "Percent of memory reserved for non-data use."},
		{Name: "databases", Value: "16", DataType: "integer", AllowedValues: "1-",
			IsModifiable: false, MinimumEngineVersion: minVer, Description: "Number of logical databases."},
		{Name: "maxclients", Value: "65000", DataType: "integer", AllowedValues: "1-",
			IsModifiable: false, MinimumEngineVersion: minVer, Description: "Maximum number of client connections."},
		{Name: "cluster-enabled", Value: "no", DataType: "string", AllowedValues: "yes,no",
			IsModifiable: false, MinimumEngineVersion: minVer, Description: "Whether cluster mode is enabled."},
	}
}

func memcachedDefaultParameters() []Parameter {
	const minVer = "1.4.14"

	return []Parameter{
		{Name: "max_item_size", Value: "1048576", DataType: "integer", AllowedValues: "1024-1048576",
			IsModifiable: true, MinimumEngineVersion: minVer, Description: "Maximum size of a stored item, in bytes."},
		{Name: "maxconns_fast", Value: "0", DataType: "boolean", AllowedValues: "0,1",
			IsModifiable: true, MinimumEngineVersion: minVer, Description: "Close new connections immediately when at the limit."},
		{Name: "lru_crawler", Value: "0", DataType: "boolean", AllowedValues: "0,1",
			IsModifiable: true, MinimumEngineVersion: minVer, Description: "Enable the LRU crawler background thread."},
		{Name: "cas_disabled", Value: "0", DataType: "boolean", AllowedValues: "0,1",
			IsModifiable: true, MinimumEngineVersion: minVer, Description: "Disable check-and-set operations."},
		{Name: "slab_automove", Value: "0", DataType: "integer", AllowedValues: "0,1,2",
			IsModifiable: true, MinimumEngineVersion: minVer, Description: "Automatic slab memory reassignment mode."},
		{Name: "chunk_size", Value: "48", DataType: "integer", AllowedValues: "1-48",
			IsModifiable: false, MinimumEngineVersion: "1.4.5", Description: "Minimum chunk size, in bytes."},
		{Name: "chunk_size_growth_factor", Value: "1.25", DataType: "float", AllowedValues: "1.0-",
			IsModifiable: false, MinimumEngineVersion: "1.4.5", Description: "Chunk size growth factor."},
		{Name: "max_simultaneous_connections", Value: "65000", DataType: "integer", AllowedValues: "1-",
			IsModifiable: false, MinimumEngineVersion: "1.4.5", Description: "Maximum simultaneous connections."},
	}
}

// defaultParameterIndex builds a name→Parameter lookup of a family's defaults.
func defaultParameterIndex(family string) map[string]Parameter {
	defaults := defaultCacheParameters(family)

	idx := make(map[string]Parameter, len(defaults))
	for _, p := range defaults {
		idx[p.Name] = p
	}

	return idx
}

// DescribeCacheParameters returns the detailed parameter list for a cache
// parameter group: the engine-family defaults with any user overrides applied
// (Source becomes "user" for a modified parameter). source, when non-empty,
// narrows the result to "user" (only modified parameters) or "system" /
// "engine-default" (only unmodified defaults). A missing group reports
// CacheParameterGroupNotFound.
func (m *Mock) DescribeCacheParameters(_ context.Context, name, source string) ([]Parameter, error) {
	pg, ok := m.parameterGroups.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "cache parameter group %q not found", name)
	}

	defaults := defaultCacheParameters(pg.Family)

	out := make([]Parameter, 0, len(defaults))

	for _, p := range defaults {
		if v, overridden := pg.Overrides[p.Name]; overridden {
			p.Value = v
			p.Source = paramSourceUser
		} else {
			p.Source = paramSourceSystem
		}

		if !matchesParameterSource(source, p.Source) {
			continue
		}

		out = append(out, p)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

// matchesParameterSource reports whether a parameter whose effective source is
// paramSource should be included given the requested source filter. An empty
// filter matches everything; "engine-default" is treated as "system".
func matchesParameterSource(filter, paramSource string) bool {
	switch filter {
	case "", paramSource:
		return true
	case paramSourceEngineDefault:
		return paramSource == paramSourceSystem
	default:
		return false
	}
}

// ModifyCacheParameterGroup applies the given name→value overrides to a cache
// parameter group. Each name must be a known parameter for the group's family
// and be modifiable; otherwise InvalidParameterValue is returned and no override
// is applied. A missing group reports CacheParameterGroupNotFound.
func (m *Mock) ModifyCacheParameterGroup(_ context.Context, name string, updates []ParameterUpdate) error {
	pg, ok := m.parameterGroups.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "cache parameter group %q not found", name)
	}

	idx := defaultParameterIndex(pg.Family)

	for _, u := range updates {
		def, known := idx[u.Name]
		if !known {
			return cerrors.Newf(cerrors.InvalidArgument, "unknown parameter %q for family %q", u.Name, pg.Family)
		}

		if !def.IsModifiable {
			return cerrors.Newf(cerrors.InvalidArgument, "parameter %q is not modifiable", u.Name)
		}
	}

	if pg.Overrides == nil {
		pg.Overrides = make(map[string]string, len(updates))
	}

	for _, u := range updates {
		pg.Overrides[u.Name] = u.Value
	}

	m.parameterGroups.Set(name, pg)

	return nil
}

// ResetCacheParameterGroup restores parameters to their engine defaults. When
// resetAll is true every override is cleared; otherwise only the named
// parameters are reset (each must be a known parameter for the family). A
// missing group reports CacheParameterGroupNotFound.
func (m *Mock) ResetCacheParameterGroup(_ context.Context, name string, resetAll bool, names []string) error {
	pg, ok := m.parameterGroups.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "cache parameter group %q not found", name)
	}

	if resetAll {
		pg.Overrides = nil
		m.parameterGroups.Set(name, pg)

		return nil
	}

	idx := defaultParameterIndex(pg.Family)
	for _, n := range names {
		if _, known := idx[n]; !known {
			return cerrors.Newf(cerrors.InvalidArgument, "unknown parameter %q for family %q", n, pg.Family)
		}
	}

	for _, n := range names {
		delete(pg.Overrides, n)
	}

	m.parameterGroups.Set(name, pg)

	return nil
}
