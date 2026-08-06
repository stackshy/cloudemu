package memorydb

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
)

func cloneMultiRegion(in *mdbdriver.MultiRegionCluster) mdbdriver.MultiRegionCluster {
	c := *in
	c.Tags = copyTags(c.Tags)
	c.Members = append([]mdbdriver.RegionalCluster(nil), c.Members...)

	return c
}

// CreateMultiRegionCluster creates a cross-region cluster. Real MemoryDB
// prefixes the name with "virtual-"; the caller supplies the suffix.
//
//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) CreateMultiRegionCluster(
	_ context.Context, cfg mdbdriver.CreateMultiRegionClusterConfig,
) (*mdbdriver.MultiRegionCluster, error) {
	if err := validName("multi-region cluster", cfg.NameSuffix); err != nil {
		return nil, err
	}

	name := "virtual-" + cfg.NameSuffix

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.multiRegion.Has(name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "multi-region cluster %q already exists", name)
	}

	c := mdbdriver.MultiRegionCluster{
		Name: name, ARN: m.arn("multiregioncluster", name), Status: mdbdriver.StatusAvailable,
		NodeType: orDefault(cfg.NodeType, defaultNodeType), Engine: orDefault(cfg.Engine, defaultEngine),
		EngineVersion: orDefault(cfg.EngineVersion, defaultEngineVersion), NumberOfShards: maxInt(cfg.NumShards, 1),
		TLSEnabled: cfg.TLSEnabled, MultiRegionParameterGroupName: cfg.MultiRegionParameterGroupName,
		Tags: copyTags(cfg.Tags),
	}
	m.multiRegion.Set(name, c)

	out := cloneMultiRegion(&c)

	return &out, nil
}

// registerMRCMember records a regional cluster as a member of a multi-region
// cluster so the delete guard is live. The caller holds the write lock; the
// MRC's existence is validated by the caller before this runs.
func (m *Mock) registerMRCMember(mrcName, clusterName, arn string) {
	mrc, ok := m.multiRegion.Get(mrcName)
	if !ok {
		return
	}

	for i := range mrc.Members {
		if mrc.Members[i].ClusterName == clusterName {
			return
		}
	}

	mrc.Members = append(mrc.Members, mdbdriver.RegionalCluster{
		ClusterName: clusterName, Region: m.opts.Region, Status: mdbdriver.StatusAvailable, ARN: arn,
	})
	m.multiRegion.Set(mrcName, mrc)
}

// unregisterMRCMember drops a regional cluster from its multi-region cluster's
// member list. The caller holds the write lock.
func (m *Mock) unregisterMRCMember(mrcName, clusterName string) {
	mrc, ok := m.multiRegion.Get(mrcName)
	if !ok {
		return
	}

	kept := mrc.Members[:0:0]

	for _, mem := range mrc.Members {
		if mem.ClusterName != clusterName {
			kept = append(kept, mem)
		}
	}

	mrc.Members = kept
	m.multiRegion.Set(mrcName, mrc)
}

// DescribeMultiRegionClusters returns all multi-region clusters, or named ones.
func (m *Mock) DescribeMultiRegionClusters(_ context.Context, names []string) ([]mdbdriver.MultiRegionCluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeByName(m.multiRegion, names, cloneMultiRegion, func(n string) error {
		return cerrors.Newf(cerrors.NotFound, "multi-region cluster %q not found", n)
	})
}

// UpdateMultiRegionCluster updates node type / engine version / shard count.
func (m *Mock) UpdateMultiRegionCluster(
	_ context.Context, name, nodeType, engineVersion string, shardCount *int,
) (*mdbdriver.MultiRegionCluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.multiRegion.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "multi-region cluster %q not found", name)
	}

	c.NodeType = orKeep(nodeType, c.NodeType)
	c.EngineVersion = orKeep(engineVersion, c.EngineVersion)

	if shardCount != nil && *shardCount > 0 {
		c.NumberOfShards = *shardCount
	}

	m.multiRegion.Set(name, c)

	out := cloneMultiRegion(&c)

	return &out, nil
}

// DeleteMultiRegionCluster removes a multi-region cluster; it must have no
// regional members still attached.
func (m *Mock) DeleteMultiRegionCluster(_ context.Context, name string) (*mdbdriver.MultiRegionCluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.multiRegion.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "multi-region cluster %q not found", name)
	}

	if len(c.Members) > 0 {
		return nil, cerrors.Newf(cerrors.FailedPrecondition, "multi-region cluster %q still has regional members", name)
	}

	m.multiRegion.Delete(name)

	c.Status = mdbdriver.StatusDeleting

	out := cloneMultiRegion(&c)

	return &out, nil
}

// ListAllowedMultiRegionClusterUpdates returns allowed node-type updates.
func (m *Mock) ListAllowedMultiRegionClusterUpdates(_ context.Context, name string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.multiRegion.Has(name) {
		return nil, cerrors.Newf(cerrors.NotFound, "multi-region cluster %q not found", name)
	}

	return []string{"db.r7g.large", "db.r7g.xlarge"}, nil
}

// DescribeMultiRegionParameterGroups returns the default multi-region parameter
// group catalog.
func (m *Mock) DescribeMultiRegionParameterGroups(_ context.Context, _ []string) ([]mdbdriver.MultiRegionParameterGroup, error) {
	return []mdbdriver.MultiRegionParameterGroup{{
		Name: "default.memorydb-multiregion-redis7", ARN: m.arn("multiregionparametergroup", "default.memorydb-multiregion-redis7"),
		Family: "memorydb_multiregion_redis7", Description: "Default multi-region parameter group",
	}}, nil
}

// DescribeMultiRegionParameters returns the effective parameters (defaults).
func (*Mock) DescribeMultiRegionParameters(_ context.Context, _ string) ([]mdbdriver.Parameter, error) {
	return []mdbdriver.Parameter{
		defaultParameters["maxmemory-policy"],
		defaultParameters["timeout"],
	}, nil
}
