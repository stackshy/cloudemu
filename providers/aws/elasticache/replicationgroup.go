package elasticache

import (
	"context"
	"fmt"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
)

// CreateReplicationGroup creates a replication group.
//
// A duplicate id reports ReplicationGroupAlreadyExists: callers re-running a
// provision treat that specific code as "already there, carry on", so folding
// it into a generic error would turn a safe re-run into a hard failure.
func (m *Mock) CreateReplicationGroup(
	_ context.Context, cfg cachedriver.ReplicationGroupConfig,
) (*cachedriver.ReplicationGroup, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "ReplicationGroupId is required")
	}

	if m.replicationGroups.Has(cfg.ID) {
		return nil, cerrors.Newf(cerrors.AlreadyExists,
			"ReplicationGroupAlreadyExists: replication group %q already exists", cfg.ID)
	}

	engine := cfg.Engine
	if engine == "" {
		engine = defaultEngine
	}

	nodeType := cfg.NodeType
	if nodeType == "" {
		nodeType = defaultNodeType
	}

	nodes := cfg.NumCacheNodes
	if nodes < 1 {
		nodes = 1
	}

	rg := cachedriver.ReplicationGroup{
		ID:            cfg.ID,
		Description:   cfg.Description,
		Status:        statusAvailable,
		Engine:        engine,
		EngineVersion: cfg.EngineVersion,
		NodeType:      nodeType,
		NumCacheNodes: nodes,
		// Callers read the primary endpoint to build a connection string; a
		// group without one is indistinguishable from a broken provision.
		PrimaryAddress: fmt.Sprintf("%s.%s.cache.amazonaws.com",
			cfg.ID, m.opts.Region),
		PrimaryPort:     defaultRedisPort,
		SubnetGroupName: cfg.SubnetGroupName,
		ARN: "arn:aws:elasticache:" + m.opts.Region + ":" + m.opts.AccountID +
			":replicationgroup:" + cfg.ID,
	}
	m.replicationGroups.Set(cfg.ID, rg)

	return &rg, nil
}

// DescribeReplicationGroups returns the named groups, or all when none given.
func (m *Mock) DescribeReplicationGroups(
	_ context.Context, ids []string,
) ([]cachedriver.ReplicationGroup, error) {
	if len(ids) == 0 {
		return m.replicationGroups.SortedValues(), nil
	}

	out := make([]cachedriver.ReplicationGroup, 0, len(ids))

	for _, id := range ids {
		rg, ok := m.replicationGroups.Get(id)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound,
				"ReplicationGroupNotFoundFault: replication group %q not found", id)
		}

		out = append(out, rg)
	}

	return out, nil
}

// ModifyReplicationGroup changes the node count of an existing group.
func (m *Mock) ModifyReplicationGroup(
	_ context.Context, id string, numCacheNodes int,
) (*cachedriver.ReplicationGroup, error) {
	rg, ok := m.replicationGroups.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound,
			"ReplicationGroupNotFoundFault: replication group %q not found", id)
	}

	if numCacheNodes > 0 {
		rg.NumCacheNodes = numCacheNodes
	}

	m.replicationGroups.Set(id, rg)

	return &rg, nil
}

// DeleteReplicationGroup deletes a replication group.
func (m *Mock) DeleteReplicationGroup(_ context.Context, id string) error {
	if !m.replicationGroups.Delete(id) {
		return cerrors.Newf(cerrors.NotFound,
			"ReplicationGroupNotFoundFault: replication group %q not found", id)
	}

	return nil
}
