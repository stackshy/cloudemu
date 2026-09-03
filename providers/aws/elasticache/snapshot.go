package elasticache

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/regionctx"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
)

// snapshotARN builds an ElastiCache snapshot ARN in the given region.
func (m *Mock) snapshotARN(region, name string) string {
	return "arn:aws:elasticache:" + region + ":" + m.opts.AccountID + ":snapshot:" + name
}

// CreateSnapshot takes a point-in-time backup of a cache cluster or replication
// group. Real ElastiCache lowercases the snapshot name and rejects a duplicate
// with SnapshotAlreadyExistsFault; the identity of the source (engine, node
// type, version, port, node count) is copied into the snapshot so a restore can
// recreate a like-for-like cluster.
func (m *Mock) CreateSnapshot(ctx context.Context, cfg cachedriver.SnapshotConfig) (*cachedriver.Snapshot, error) {
	name := strings.ToLower(cfg.SnapshotName)
	if name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "SnapshotName is required")
	}

	if m.snapshots.Has(name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists,
			"SnapshotAlreadyExistsFault: snapshot %q already exists", name)
	}

	// A snapshot is a child of the cluster or replication group it backs up, so
	// it inherits that source's region. The request region is only a fallback for
	// the (AWS-invalid) case where no source is given.
	region := regionctx.RegionOr(ctx, m.opts.Region)
	snap := cachedriver.Snapshot{
		Name:          name,
		Status:        statusAvailable,
		Source:        "manual",
		Port:          defaultRedisPort,
		NumCacheNodes: 1,
		CreatedAt:     m.opts.Clock.Now().UTC(),
		ARN:           m.snapshotARN(region, name),
	}

	if err := m.fillSnapshotSource(cfg, &snap, region); err != nil {
		return nil, err
	}

	m.snapshots.Set(name, snap)

	return &snap, nil
}

// fillSnapshotSource resolves the snapshot's source cluster or replication group
// and copies its identity onto snap, including stamping the snapshot's ARN with
// the source's region (from its stored ARN) so the child inherits the parent's
// region rather than the request's. fallbackRegion is used only if the source's
// stored ARN is malformed.
func (m *Mock) fillSnapshotSource(cfg cachedriver.SnapshotConfig, snap *cachedriver.Snapshot, fallbackRegion string) error {
	switch {
	case cfg.CacheClusterID != "":
		cd, ok := m.caches.Get(cfg.CacheClusterID)
		if !ok {
			return cerrors.Newf(cerrors.NotFound,
				"CacheClusterNotFound: cache cluster %q not found", cfg.CacheClusterID)
		}

		if err := checkSnapshotSupported(cd.info.Engine, cd.info.NodeType); err != nil {
			return err
		}

		snap.CacheClusterID = cfg.CacheClusterID
		snap.Engine = cd.info.Engine
		snap.EngineVersion = cd.info.EngineVersion
		snap.NodeType = cd.info.NodeType
		snap.ParameterGroupName = paramGroupName(cd.info.Engine)
		snap.ARN = m.snapshotARN(arnRegion(cd.info.ARN, fallbackRegion), snap.Name)

		if cd.info.NumCacheNodes > 0 {
			snap.NumCacheNodes = cd.info.NumCacheNodes
		}
	case cfg.ReplicationGroupID != "":
		rg, ok := m.replicationGroups.Get(cfg.ReplicationGroupID)
		if !ok {
			return cerrors.Newf(cerrors.NotFound,
				"ReplicationGroupNotFoundFault: replication group %q not found", cfg.ReplicationGroupID)
		}

		snap.ReplicationGroupID = cfg.ReplicationGroupID
		snap.Engine = rg.Engine
		snap.EngineVersion = rg.EngineVersion
		snap.NodeType = rg.NodeType
		snap.ParameterGroupName = paramGroupName(rg.Engine)
		snap.ARN = m.snapshotARN(arnRegion(rg.ARN, fallbackRegion), snap.Name)

		if rg.PrimaryPort != 0 {
			snap.Port = rg.PrimaryPort
		}
	}

	return nil
}

// checkSnapshotSupported enforces that snapshots are valid for Valkey/Redis OSS
// only. Real ElastiCache rejects a snapshot of a Memcached cluster or one
// running on a cache.t1.micro node with SnapshotFeatureNotSupportedFault.
func checkSnapshotSupported(engine, nodeType string) error {
	if engine == engineMemcached {
		return cerrors.New(cerrors.FailedPrecondition,
			"SnapshotFeatureNotSupportedFault: snapshots are not supported for Memcached clusters")
	}

	if nodeType == "cache.t1.micro" {
		return cerrors.New(cerrors.FailedPrecondition,
			"SnapshotFeatureNotSupportedFault: snapshots are not supported for cache.t1.micro nodes")
	}

	return nil
}

// paramGroupName derives the default cache parameter group name for an engine,
// e.g. "default.redis". Empty for an unspecified engine.
func paramGroupName(engine string) string {
	if engine == "" {
		return ""
	}

	return "default." + engine
}

// CopySnapshot deep-copies an existing snapshot to a new name. Real ElastiCache
// lowercases both names, rejects a missing source with SnapshotNotFoundFault, and
// a duplicate target with SnapshotAlreadyExistsFault. The copy is a wholly
// independent record: the driver.Snapshot value is copied by value (it holds no
// slice/map fields), stamped with a fresh name/ARN/create-time and Source
// "copied", so later mutation of either snapshot cannot corrupt the other.
func (m *Mock) CopySnapshot(ctx context.Context, cfg cachedriver.CopySnapshotConfig) (*cachedriver.Snapshot, error) {
	source := strings.ToLower(cfg.SourceSnapshotName)
	target := strings.ToLower(cfg.TargetSnapshotName)

	if target == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "TargetSnapshotName is required")
	}

	src, ok := m.snapshots.Get(source)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound,
			"SnapshotNotFoundFault: snapshot %q not found", source)
	}

	region := regionctx.RegionOr(ctx, m.opts.Region)

	dst := src
	dst.Name = target
	dst.Source = "copied"
	dst.Status = statusAvailable
	dst.CreatedAt = m.opts.Clock.Now().UTC()
	dst.ARN = m.snapshotARN(arnRegion(src.ARN, region), target)

	// SetIfAbsent is the atomic create-if-new guard: a concurrent copy to the same
	// target loses the race and reports the duplicate, never silently overwrites.
	if !m.snapshots.SetIfAbsent(target, dst) {
		return nil, cerrors.Newf(cerrors.AlreadyExists,
			"SnapshotAlreadyExistsFault: snapshot %q already exists", target)
	}

	return &dst, nil
}

// DeleteSnapshot removes a snapshot and returns its last state marked
// "deleting", as real ElastiCache does (the delete is asynchronous there). A
// missing snapshot reports SnapshotNotFoundFault.
func (m *Mock) DeleteSnapshot(_ context.Context, name string) (*cachedriver.Snapshot, error) {
	key := strings.ToLower(name)

	snap, ok := m.snapshots.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound,
			"SnapshotNotFoundFault: snapshot %q not found", key)
	}

	// Gate the response on the delete winning: if a concurrent caller removed the
	// snapshot between the Get and here, report the not-found it now is.
	if !m.snapshots.Delete(key) {
		return nil, cerrors.Newf(cerrors.NotFound,
			"SnapshotNotFoundFault: snapshot %q not found", key)
	}

	snap.Status = "deleting"

	return &snap, nil
}

// restoreSeed seeds the config fields of a cache cluster or replication group
// restore from the named snapshot, filling only the fields the request left
// unset (an explicit request value always wins). A blank name is not a restore
// (nil, nil); a named-but-missing snapshot reports SnapshotNotFoundFault.
func (m *Mock) restoreSeed(name string) (*cachedriver.Snapshot, error) {
	key := strings.ToLower(name)
	if key == "" {
		return nil, nil
	}

	snap, ok := m.snapshots.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound,
			"SnapshotNotFoundFault: snapshot %q not found", key)
	}

	return &snap, nil
}

// seedCacheRestore fills a CreateCacheCluster config from its SnapshotName, so a
// restore reproduces the source cluster's shape unless the request overrides a
// field. It is a no-op when the request names no snapshot.
func (m *Mock) seedCacheRestore(cfg *cachedriver.CacheConfig) error {
	snap, err := m.restoreSeed(cfg.SnapshotName)
	if err != nil || snap == nil {
		return err
	}

	if cfg.Engine == "" {
		cfg.Engine = snap.Engine
	}

	if cfg.NodeType == "" {
		cfg.NodeType = snap.NodeType
	}

	if cfg.EngineVersion == "" {
		cfg.EngineVersion = snap.EngineVersion
	}

	if cfg.NumCacheNodes == 0 {
		cfg.NumCacheNodes = snap.NumCacheNodes
	}

	if cfg.Port == 0 {
		cfg.Port = snap.Port
	}

	return nil
}

// seedReplicationGroupRestore fills a CreateReplicationGroup config from its
// SnapshotName, mirroring seedCacheRestore for the replication-group surface.
func (m *Mock) seedReplicationGroupRestore(cfg *cachedriver.ReplicationGroupConfig) error {
	snap, err := m.restoreSeed(cfg.SnapshotName)
	if err != nil || snap == nil {
		return err
	}

	if cfg.Engine == "" {
		cfg.Engine = snap.Engine
	}

	if cfg.NodeType == "" {
		cfg.NodeType = snap.NodeType
	}

	if cfg.EngineVersion == "" {
		cfg.EngineVersion = snap.EngineVersion
	}

	if cfg.NumCacheNodes == 0 {
		cfg.NumCacheNodes = snap.NumCacheNodes
	}

	return nil
}

// DescribeSnapshots returns all snapshots, narrowed by the non-empty filter
// fields. An explicit SnapshotName that matches nothing reports
// SnapshotNotFoundFault, as real ElastiCache does.
func (m *Mock) DescribeSnapshots(
	_ context.Context, filter cachedriver.SnapshotFilter,
) ([]cachedriver.Snapshot, error) {
	if name := strings.ToLower(filter.SnapshotName); name != "" {
		snap, ok := m.snapshots.Get(name)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound,
				"SnapshotNotFoundFault: snapshot %q not found", name)
		}

		return []cachedriver.Snapshot{snap}, nil
	}

	all := m.snapshots.SortedValues()

	out := make([]cachedriver.Snapshot, 0, len(all))

	for i := range all {
		if filter.CacheClusterID != "" && all[i].CacheClusterID != filter.CacheClusterID {
			continue
		}

		if filter.ReplicationGroupID != "" && all[i].ReplicationGroupID != filter.ReplicationGroupID {
			continue
		}

		out = append(out, all[i])
	}

	return out, nil
}
