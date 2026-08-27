package rds

import (
	"context"
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
	arnFieldCount            = 7
)

// engineVersionCatalog is a representative set of supported versions per
// engine. Real AWS returns a far larger, region-specific list; the emulator
// returns a stable subset.
//
//nolint:gochecknoglobals // static lookup table
var engineVersionCatalog = map[string][]string{
	"mysql":             {"8.0.35", "8.0.36"},
	"postgres":          {"15.4", "16.2"},
	"mariadb":           {"10.11.6"},
	"aurora-mysql":      {"8.0.mysql_aurora.3.05.2"},
	"aurora-postgresql": {"15.4", "16.1"},
	"docdb":             {"5.0.0"},
	"neptune":           {"1.3.1.0"},
}

// engineFamily maps an engine to its parameter-group family prefix.
//
//nolint:gochecknoglobals // static lookup table
var engineFamily = map[string]string{
	"mysql":             "mysql8.0",
	"postgres":          "postgres16",
	"mariadb":           "mariadb10.11",
	"aurora-mysql":      "aurora-mysql8.0",
	"aurora-postgresql": "aurora-postgresql16",
	"docdb":             "docdb5.0",
	"neptune":           "neptune1.3",
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
// EngineVersion — the first (lowest) version the emulator lists for the engine.
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
				DBParameterGroupFamily: engineFamily[eng],
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
	"mysql", "postgres", "mariadb", "aurora-mysql", "aurora-postgresql", "docdb", "neptune",
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
// not records and always report false — their tags live in groupTags.
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

// isGroupKind reports whether an ARN kind is a parameter/option/subnet group.
func isGroupKind(kind string) bool {
	switch kind {
	case arnKindParamGroup, arnKindClusterParamGroup, arnKindOptionGroup, arnKindSubnetGroup:
		return true
	default:
		return false
	}
}

// groupExists reports whether a parameter/option/subnet group named id exists.
func (m *Mock) groupExists(kind, id string) bool {
	switch kind {
	case arnKindParamGroup:
		return m.paramGroups.Has(id)
	case arnKindClusterParamGroup:
		return m.clusterParamGroups.Has(id)
	case arnKindOptionGroup:
		return m.optionGroups.Has(id)
	case arnKindSubnetGroup:
		return m.subnetGroups.Has(id)
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
