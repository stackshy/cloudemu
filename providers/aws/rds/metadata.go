package rds

import (
	"context"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

var (
	_ rdsdriver.Metadata = (*Mock)(nil)
	_ rdsdriver.Tagging  = (*Mock)(nil)
)

// ARN resource-kind segments and the fixed RDS ARN field count.
const (
	arnKindInstance          = "db"
	arnKindCluster           = "cluster"
	arnKindSnapshot          = "snapshot"
	arnKindClusterSnapshot   = "cluster-snapshot"
	arnKindParamGroup        = "pg"
	arnKindClusterParamGroup = "cluster-pg"
	arnKindOptionGroup       = "og"
	arnKindSubnetGroup       = "subgrp"
	arnKindEventSubscription = "es"
	arnFieldCount            = 7
)

// engineVersionCatalog is a representative set of supported versions per
// engine. Real AWS returns a far larger, region-specific list; the emulator
// returns a stable subset.
//
//nolint:gochecknoglobals // static lookup table
var engineVersionCatalog = map[string][]string{
	engineMySQL:            {"8.0.35", "8.0.36"},
	enginePostgres:         {"15.4", "16.2"},
	engineMariaDB:          {"10.11.6"},
	engineAuroraMySQL:      {"8.0.mysql_aurora.3.05.2"},
	engineAuroraPostgreSQL: {"15.4", "16.1"},
	engineDocDB:            {"5.0.0"},
	engineNeptune:          {"1.3.1.0"},
}

// Engine names whose parameter-group family is derived from the engine version.
const (
	engineMySQL            = "mysql"
	enginePostgres         = "postgres"
	engineMariaDB          = "mariadb"
	engineAuroraMySQL      = "aurora-mysql"
	engineAuroraPostgreSQL = "aurora-postgresql"
)

// Version thresholds for the family naming rules below.
const (
	// postgresMajorOnlyFrom is the first PostgreSQL major whose family is named
	// by major alone (postgres10, postgres15); earlier majors use major.minor
	// (postgres9.6).
	postgresMajorOnlyFrom = 10
	// neptuneMinorFamilyFrom is the Neptune 1.x minor (1.2.0.0) from which the
	// family carries major.minor (neptune1.2, neptune1.3); earlier releases share
	// the "neptune1" family.
	neptuneMinorFamilyFrom = 2
	// familyVersionFields is the number of leading version fields a family
	// needs (major.minor).
	familyVersionFields = 2
)

// parameterGroupFamily returns the DB parameter group family real RDS reports
// (DescribeDBEngineVersions.DBParameterGroupFamily) and uses for the default
// "default.<family>" group of an engine at a given engine version. The family
// tracks the version, not just the engine: PostgreSQL 15.4 belongs to
// postgres15 while 16.2 belongs to postgres16. The naming per engine follows
// the DBParameterGroupFamily values RDS documents (e.g. postgres14, mysql8.0,
// mariadb10.6, aurora-mysql8.0, aurora-postgresql14, oracle-ee-19,
// oracle-se2-cdb-21, sqlserver-ee-15.0, db2-ae, neptune1.2, docdb5.0):
//
//	postgres / aurora-postgresql: <engine><major> (major >= 10), else <engine><major>.<minor>
//	mysql / mariadb / docdb:      <engine><major>.<minor>
//	aurora-mysql:                 aurora-mysql<major>.<minor> ("8.0.mysql_aurora.3.05.2" -> 8.0)
//	neptune:                      neptune1 before 1.2.0.0, else neptune<major>.<minor>
//	oracle-*:                     <engine>-<major> ("19.0.0.0.ru-2024-01.rur-2024-01.r1" -> 19)
//	sqlserver-*:                  <engine>-<major>.<minor> ("15.00.4365.2.v1" -> 15.0)
//	db2-*:                        <engine> (one family per edition)
//
// An empty version resolves to the engine's default version first. An unknown
// engine, or a version that does not parse, yields "" (no known family).
func parameterGroupFamily(engine, version string) string {
	if strings.HasPrefix(engine, "db2-") {
		return engine
	}

	if version == "" {
		version = defaultEngineVersion(engine)
	}

	major, minor, ok := versionMajorMinor(version)
	if !ok {
		return ""
	}

	return versionedFamily(engine, major, minor)
}

// versionedFamily names the family of an engine whose family carries its
// version (see parameterGroupFamily), or "" for an unknown engine.
func versionedFamily(engine string, major, minor int) string {
	majorStr := strconv.Itoa(major)
	majorMinor := majorStr + "." + strconv.Itoa(minor)

	switch {
	case engine == enginePostgres || engine == engineAuroraPostgreSQL:
		if major < postgresMajorOnlyFrom {
			return engine + majorMinor
		}

		return engine + majorStr
	case engine == engineMySQL, engine == engineMariaDB, engine == engineDocDB, engine == engineAuroraMySQL:
		return engine + majorMinor
	case engine == engineNeptune:
		return neptuneFamily(major, minor)
	case strings.HasPrefix(engine, "oracle-"):
		return engine + "-" + majorStr
	case strings.HasPrefix(engine, "sqlserver-"):
		return engine + "-" + majorMinor
	default:
		return ""
	}
}

// isKnownFamily reports whether family is a DB parameter group family that
// parameterGroupFamily produces for some engine and version: the family names
// a recognized engine followed by a version it could come from (postgres16,
// mysql8.0, aurora-postgresql15, oracle-ee-19, sqlserver-ee-15.0, neptune1,
// db2-se). It rejects names such as "bogus", "postgres16.2" or
// "postgres-16", so "default.<family>" groups resolve only for real families.
func isKnownFamily(family string) bool {
	for engine := range validEngines {
		if familyFromEngine(engine, family) {
			return true
		}
	}

	return false
}

// familyFromEngine reports whether family is the family of engine at the
// version spelled by the rest of the name ("16" or "16.0" for postgres16).
func familyFromEngine(engine, family string) bool {
	if family == engine {
		return parameterGroupFamily(engine, "") == family
	}

	rest, ok := strings.CutPrefix(family, engine)
	if !ok {
		return false
	}

	rest = strings.TrimPrefix(rest, "-")
	if !strings.Contains(rest, ".") {
		rest += ".0"
	}

	return parameterGroupFamily(engine, rest) == family
}

// neptuneFamily returns the Neptune family: neptune1 for every release before
// 1.2.0.0, then neptune<major>.<minor>.
func neptuneFamily(major, minor int) string {
	if major == 1 && minor < neptuneMinorFamilyFrom {
		return "neptune1"
	}

	return engineNeptune + strconv.Itoa(major) + "." + strconv.Itoa(minor)
}

// versionMajorMinor parses the leading numeric major.minor fields of an engine
// version ("15.4" -> 15, 4; "8.0.mysql_aurora.3.05.2" -> 8, 0).
func versionMajorMinor(version string) (major, minor int, ok bool) {
	parts := strings.Split(version, ".")
	if len(parts) < familyVersionFields {
		return 0, 0, false
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}

	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}

	return major, minor, true
}

// orderableInstanceClasses is the representative set of instance classes the
// emulator reports as orderable for any engine.
//
//nolint:gochecknoglobals // static lookup table
var orderableInstanceClasses = []string{
	"db.t3.micro", "db.t3.small", "db.t3.medium",
	"db.r5.large", "db.r5.xlarge", "db.m5.large",
}

// defaultEngineVersion returns the version real RDS assigns when a caller omits
// EngineVersion, the first (lowest) version the emulator lists for the engine.
// Returns "" for an unknown engine, leaving the field unset.
func defaultEngineVersion(engine string) string {
	versions := engineVersionCatalog[engine]
	if len(versions) == 0 {
		return ""
	}

	return versions[0]
}

func (*Mock) DescribeDBEngineVersions(_ context.Context, engine, engineVersion string) ([]rdsdriver.DBEngineVersion, error) {
	out := make([]rdsdriver.DBEngineVersion, 0)

	for _, eng := range engineCatalogOrder {
		if engine != "" && eng != engine {
			continue
		}

		for _, v := range engineVersionCatalog[eng] {
			if engineVersion != "" && v != engineVersion {
				continue
			}

			out = append(out, rdsdriver.DBEngineVersion{
				Engine:                 eng,
				EngineVersion:          v,
				DBEngineDescription:    "Amazon RDS " + eng,
				DBParameterGroupFamily: parameterGroupFamily(eng, v),
			})
		}
	}

	return out, nil
}

func (*Mock) DescribeOrderableDBInstanceOptions(
	_ context.Context, engine, engineVersion string,
) ([]rdsdriver.OrderableDBInstanceOption, error) {
	if engine == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "Engine is required")
	}

	versions := engineVersionCatalog[engine]
	if engineVersion != "" {
		versions = []string{engineVersion}
	}

	out := make([]rdsdriver.OrderableDBInstanceOption, 0, len(versions)*len(orderableInstanceClasses))

	for _, v := range versions {
		for _, class := range orderableInstanceClasses {
			out = append(out, rdsdriver.OrderableDBInstanceOption{
				Engine:          engine,
				EngineVersion:   v,
				DBInstanceClass: class,
				StorageType:     "gp3",
				MultiAZCapable:  true,
			})
		}
	}

	return out, nil
}

// engineCatalogOrder fixes iteration order for deterministic output.
//
//nolint:gochecknoglobals // static lookup table
var engineCatalogOrder = []string{
	engineMySQL, enginePostgres, engineMariaDB, engineAuroraMySQL, engineAuroraPostgreSQL, engineDocDB, engineNeptune,
}

// ---- tagging ----

// resourceKind identifies the tagged resource type parsed from an ARN.
func arnResourceKindID(arn string) (kind, id string) {
	// arn:aws:rds:<region>:<acct>:<kind>:<id>
	parts := strings.SplitN(arn, ":", arnFieldCount)
	if len(parts) < arnFieldCount {
		return "", ""
	}

	return parts[5], parts[6]
}

func (m *Mock) AddTagsToResource(_ context.Context, resourceARN string, tags map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.withResourceTags(resourceARN, func(existing map[string]string) map[string]string {
		// Build a fresh map rather than mutating the stored one in place: a
		// concurrent Describe may still be reading it (copy-on-read + replace-
		// on-write, mirroring ModifyInstance).
		out := copyTags(existing)
		if out == nil {
			out = map[string]string{}
		}

		for k, v := range tags {
			out[k] = v
		}

		return out
	})
}

func (m *Mock) RemoveTagsFromResource(_ context.Context, resourceARN string, keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.withResourceTags(resourceARN, func(existing map[string]string) map[string]string {
		out := copyTags(existing)
		for _, k := range keys {
			delete(out, k)
		}

		return out
	})
}

func (m *Mock) ListTagsForResource(_ context.Context, resourceARN string) (map[string]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	kind, id := arnResourceKindID(resourceARN)

	if tags, ok := m.recordTags(kind, id); ok {
		return tags, nil
	}

	if isGroupKind(kind) && m.groupExists(kind, id) {
		return copyTags(m.groupTags[resourceARN]), nil
	}

	return nil, errUntaggable(resourceARN)
}

// recordTags returns a copy of the tags on the record named by kind+id, and
// whether that record exists. Group kinds (parameter/option/subnet groups) are
// not records and always report false. Their tags live in groupTags.
func (m *Mock) recordTags(kind, id string) (map[string]string, bool) {
	switch kind {
	case arnKindInstance:
		if r, ok := m.instances.Get(id); ok {
			return copyTags(r.Tags), true
		}
	case arnKindCluster:
		if r, ok := m.clusters.Get(id); ok {
			return copyTags(r.Tags), true
		}
	case arnKindSnapshot:
		if r, ok := m.snapshots.Get(id); ok {
			return copyTags(r.Tags), true
		}
	case arnKindClusterSnapshot:
		if r, ok := m.clusterSnapshots.Get(id); ok {
			return copyTags(r.Tags), true
		}
	}

	return nil, false
}

// isGroupKind reports whether an ARN kind keeps its tags in groupTags (keyed
// by ARN) rather than on its record: parameter/option/subnet groups and event
// subscriptions.
func isGroupKind(kind string) bool {
	switch kind {
	case arnKindParamGroup, arnKindClusterParamGroup, arnKindOptionGroup, arnKindSubnetGroup,
		arnKindEventSubscription:
		return true
	default:
		return false
	}
}

// groupExists reports whether a parameter/option/subnet group or event
// subscription named id exists. A "default.<family>" DB / cluster parameter
// group always exists (DescribeDB[Cluster]ParameterGroups resolves it), so it
// is taggable like a stored group.
func (m *Mock) groupExists(kind, id string) bool {
	_, isDefault := defaultGroupFamily(id)

	switch kind {
	case arnKindParamGroup:
		return m.paramGroups.Has(id) || isDefault
	case arnKindClusterParamGroup:
		return m.clusterParamGroups.Has(id) || isDefault
	case arnKindOptionGroup:
		return m.optionGroups.Has(id)
	case arnKindSubnetGroup:
		return m.subnetGroups.Has(id)
	case arnKindEventSubscription:
		return m.eventSubs.Has(id)
	default:
		return false
	}
}

// withResourceTags loads the tag map of the resource named by arn, applies fn,
// and stores the result. Only the resource kinds that carry tags are
// supported; others return NotFound.
func (m *Mock) withResourceTags(arn string, fn func(map[string]string) map[string]string) error {
	kind, id := arnResourceKindID(arn)

	if isGroupKind(kind) {
		if !m.groupExists(kind, id) {
			return errUntaggable(arn)
		}

		m.setGroupTags(arn, fn(m.groupTags[arn]))

		return nil
	}

	if !m.setRecordTags(kind, id, fn) {
		return errUntaggable(arn)
	}

	return nil
}

// setRecordTags applies fn to the record named by kind+id and stores it,
// returning whether the record kind was recognized and the record existed.
func (m *Mock) setRecordTags(kind, id string, fn func(map[string]string) map[string]string) bool {
	switch kind {
	case arnKindInstance:
		r, ok := m.instances.Get(id)
		if !ok {
			return false
		}

		r.Tags = fn(r.Tags)
		m.instances.Set(id, r)
	case arnKindCluster:
		r, ok := m.clusters.Get(id)
		if !ok {
			return false
		}

		r.Tags = fn(r.Tags)
		m.clusters.Set(id, r)
	case arnKindSnapshot:
		r, ok := m.snapshots.Get(id)
		if !ok {
			return false
		}

		r.Tags = fn(r.Tags)
		m.snapshots.Set(id, r)
	case arnKindClusterSnapshot:
		r, ok := m.clusterSnapshots.Get(id)
		if !ok {
			return false
		}

		r.Tags = fn(r.Tags)
		m.clusterSnapshots.Set(id, r)
	default:
		return false
	}

	return true
}

// setGroupTags stores (or clears) the tags for a group ARN.
func (m *Mock) setGroupTags(arn string, tags map[string]string) {
	if len(tags) == 0 {
		delete(m.groupTags, arn)
		return
	}

	m.groupTags[arn] = tags
}

func errUntaggable(arn string) error {
	return cerrors.Newf(cerrors.NotFound, "DB instance resource %q not found", arn)
}
