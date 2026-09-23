package elasticache

import (
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

const (
	engineRedis  = "redis"
	engineValkey = "valkey"

	defaultGroupPrefix  = "default."
	clusterOnSuffix     = ".cluster.on"
	redisSplitMajor     = 6
	maxReplicationNodes = 6
)

// knownFamilies lists the parameter group families real ElastiCache ships a
// default group for.
func knownFamilies() []string {
	return []string{
		"memcached1.4", "memcached1.5", "memcached1.6",
		"redis2.6", "redis2.8", "redis3.2", "redis4.0", "redis5.0", "redis6.x", "redis7",
		"valkey7", "valkey8",
	}
}

// validateEngine checks the engine against the ones ElastiCache supports.
// A replication group can't use memcached.
func validateEngine(engine string, replicationGroup bool) error {
	switch strings.ToLower(engine) {
	case engineRedis, engineValkey:
		return nil
	case engineMemcached:
		if !replicationGroup {
			return nil
		}
	}

	return cerrors.Newf(cerrors.InvalidArgument, "Invalid value for Engine: %s", engine)
}

// defaultFamily returns the parameter group family for an engine and version.
// Redis 6 uses "redis6.x", later Redis and Valkey use the major version, and
// older Redis and Memcached use major.minor.
func defaultFamily(engine, version string) string {
	engine = strings.ToLower(engine)
	parts := strings.Split(version, ".")
	major := parts[0]

	minor := "0"
	if len(parts) > 1 {
		minor = parts[1]
	}

	switch {
	case major == "":
		return engine
	case engine == engineMemcached:
		return engine + major + "." + minor
	case engine == engineValkey:
		return engine + major
	case major == "6":
		return engine + "6.x"
	case len(major) == 1 && major[0] < '0'+redisSplitMajor:
		return engine + major + "." + minor
	default:
		return engine + major
	}
}

// DefaultParameterGroupName is the name of the default group ElastiCache gives
// a cluster that didn't ask for one, such as "default.redis7".
func DefaultParameterGroupName(engine, version string) string {
	if engine == "" {
		return ""
	}

	return defaultGroupPrefix + defaultFamily(engine, version)
}

// defaultGroupFamily reports whether name is a built-in default group and
// returns its family. "default.redis7.cluster.on" maps to "redis7".
func defaultGroupFamily(name string) (string, bool) {
	rest, ok := strings.CutPrefix(name, defaultGroupPrefix)
	if !ok {
		return "", false
	}

	family, clusterOn := strings.CutSuffix(rest, clusterOnSuffix)

	for _, f := range knownFamilies() {
		if family == f && (!clusterOn || hasClusterOnDefault(f)) {
			return f, true
		}
	}

	return "", false
}

// hasClusterOnDefault reports whether a family also ships a
// "default.<family>.cluster.on" group. Memcached and Redis before 3.2 don't.
func hasClusterOnDefault(family string) bool {
	return !strings.HasPrefix(family, engineMemcached) && family != "redis2.6" && family != "redis2.8"
}

// defaultGroupNames lists every built-in default parameter group name.
func defaultGroupNames() []string {
	var out []string

	for _, f := range knownFamilies() {
		out = append(out, defaultGroupPrefix+f)
		if hasClusterOnDefault(f) {
			out = append(out, defaultGroupPrefix+f+clusterOnSuffix)
		}
	}

	return out
}

// lookupParameterGroup returns a stored group, or the built-in default group
// when name is one.
func (m *Mock) lookupParameterGroup(name string) (ParameterGroup, bool) {
	if pg, ok := m.parameterGroups.Get(name); ok {
		return pg, true
	}

	family, ok := defaultGroupFamily(name)
	if !ok {
		return ParameterGroup{}, false
	}

	return ParameterGroup{Name: name, Family: family, Description: "Default parameter group for " + family}, true
}

// requireParameterGroup rejects a create that names a parameter group that
// doesn't exist. An empty name means the engine default.
func (m *Mock) requireParameterGroup(name string) error {
	if name == "" {
		return nil
	}

	if _, ok := m.lookupParameterGroup(name); !ok {
		return cerrors.Newf(cerrors.NotFound, "cache parameter group %q not found", name)
	}

	return nil
}

// rejectDefaultGroupChange refuses to change or delete a built-in default group.
func rejectDefaultGroupChange(name string) error {
	if _, ok := defaultGroupFamily(name); ok {
		return cerrors.Newf(cerrors.InvalidArgument, "default cache parameter group %q can't be modified or deleted", name)
	}

	return nil
}

// validateReplicationNodeCount checks NumCacheClusters. A replication group has
// a primary and at most 5 replicas. Zero means unset and is defaulted.
func validateReplicationNodeCount(n int) error {
	if n < 0 || n > maxReplicationNodes {
		return cerrors.Newf(cerrors.InvalidArgument,
			"NumCacheClusters must be between 1 and %d", maxReplicationNodes)
	}

	return nil
}
