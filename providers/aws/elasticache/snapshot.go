package elasticache

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
)

// snapshotARN builds an ElastiCache snapshot ARN.
func (m *Mock) snapshotARN(name string) string {
	return "arn:aws:elasticache:" + m.opts.Region + ":" + m.opts.AccountID + ":snapshot:" + name
}

// CreateSnapshot takes a point-in-time backup of a cache cluster or replication
// group. Real ElastiCache lowercases the snapshot name and rejects a duplicate
// with SnapshotAlreadyExistsFault; the identity of the source (engine, node
// type, version, port, node count) is copied into the snapshot so a restore can
// recreate a like-for-like cluster.
func (m *Mock) CreateSnapshot(_ context.Context, cfg cachedriver.SnapshotConfig) (*cachedriver.Snapshot, error) {
	name := strings.ToLower(cfg.SnapshotName)
	if name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "SnapshotName is required")
	}

	if m.snapshots.Has(name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists,
			"SnapshotAlreadyExistsFault: snapshot %q already exists", name)
	}

	snap := cachedriver.Snapshot{
		Name:          name,
		Status:        statusAvailable,
		Source:        "manual",
		Port:          defaultRedisPort,
		NumCacheNodes: 1,
		CreatedAt:     m.opts.Clock.Now().UTC(),
		ARN:           m.snapshotARN(name),
	}

	if err := m.fillSnapshotSource(cfg, &snap); err != nil {
		return nil, err
	}

	m.snapshots.Set(name, snap)

	return &snap, nil
}

// fillSnapshotSource resolves the snapshot's source cluster or replication group
// and copies its identity onto snap.
func (m *Mock) fillSnapshotSource(cfg cachedriver.SnapshotConfig, snap *cachedriver.Snapshot) error {
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
