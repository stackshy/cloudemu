package elasticache

import (
	"context"
	"fmt"
	"net"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cacheengine "github.com/stackshy/cloudemu/v2/services/cache/cacheengine"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
)

// CreateReplicationGroup creates a replication group.
//
// A duplicate id reports ReplicationGroupAlreadyExists: callers re-running a
// provision treat that specific code as "already there, carry on", so folding
// it into a generic error would turn a safe re-run into a hard failure.
func (m *Mock) CreateReplicationGroup(
	ctx context.Context, cfg cachedriver.ReplicationGroupConfig,
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

	// Opt-in: back the primary with a real Redis server, replacing the synthetic
	// hostname with the host:port a client connects to. Routes through the same
	// cacheengine helper the cache-cluster path uses so the two cannot drift.
	if err := m.provisionPrimaryEndpoint(ctx, &rg); err != nil {
		return nil, err
	}

	m.replicationGroups.Set(cfg.ID, rg)

	return &rg, nil
}

// provisionPrimaryEndpoint backs the replication group's primary with the
// configured cache engine (if any), overriding the synthetic primary endpoint
// with the real host:port a Redis client connects to. It is a no-op when no
// engine is configured or the engine is not Redis-family.
func (m *Mock) provisionPrimaryEndpoint(ctx context.Context, rg *cachedriver.ReplicationGroup) error {
	info := cachedriver.CacheInfo{
		Name:     rg.ID,
		Engine:   rg.Engine,
		Endpoint: net.JoinHostPort(rg.PrimaryAddress, strconv.Itoa(rg.PrimaryPort)),
	}

	if err := cacheengine.Provision(ctx, m.opts.CacheEngine, &info); err != nil {
		return err
	}

	host, port, err := splitEndpoint(info.Endpoint)
	if err != nil {
		return err
	}

	rg.PrimaryAddress = host
	rg.PrimaryPort = port

	return nil
}

// splitEndpoint parses a "host:port" cache endpoint into its parts.
func splitEndpoint(endpoint string) (host string, port int, err error) {
	host, portStr, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", 0, cerrors.Newf(cerrors.Internal, "parse cache endpoint %q: %v", endpoint, err)
	}

	port, err = strconv.Atoi(portStr)
	if err != nil {
		return "", 0, cerrors.Newf(cerrors.Internal, "parse cache port %q: %v", portStr, err)
	}

	return host, port, nil
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

// DeleteReplicationGroup deletes a replication group, tearing down the real
// Redis server backing its primary (if any) via the shared cacheengine helper.
func (m *Mock) DeleteReplicationGroup(ctx context.Context, id string) error {
	rg, ok := m.replicationGroups.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound,
			"ReplicationGroupNotFoundFault: replication group %q not found", id)
	}

	info := cachedriver.CacheInfo{Name: rg.ID, Engine: rg.Engine}
	if err := cacheengine.Deprovision(ctx, m.opts.CacheEngine, &info); err != nil {
		return err
	}

	m.replicationGroups.Delete(id)

	return nil
}
