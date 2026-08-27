package elasticache

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
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

	engineVersion := cfg.EngineVersion
	if engineVersion == "" {
		engineVersion = defaultEngineVersion(engine)
	}

	rg := cachedriver.ReplicationGroup{
		ID:            cfg.ID,
		Description:   cfg.Description,
		Status:        statusAvailable,
		Engine:        engine,
		EngineVersion: engineVersion,
		NodeType:      nodeType,
		NumCacheNodes: nodes,
		// Callers read the primary endpoint to build a connection string; a
		// group without one is indistinguishable from a broken provision.
		PrimaryAddress: fmt.Sprintf("%s.%s.cache.amazonaws.com",
			cfg.ID, m.opts.Region),
		PrimaryPort: defaultRedisPort,
		// The reader endpoint lets clients scale reads across the replicas.
		ReaderAddress: fmt.Sprintf("%s-ro.%s.cache.amazonaws.com",
			cfg.ID, m.opts.Region),
		ReaderPort:        defaultRedisPort,
		MemberClusters:    memberClusters(cfg.ID, nodes),
		AutomaticFailover: failoverStatus(cfg.AutomaticFailoverEnabled),
		SubnetGroupName:   cfg.SubnetGroupName,
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

// maxReplicationGroupNodes bounds the member-cluster count a replication group
// can synthesize. Real ElastiCache tops out well below this (a node group holds
// a primary plus at most 5 replicas, and the default per-group node cap is far
// smaller); the ceiling exists only so a pathological NumCacheNodes cannot drive
// an unbounded allocation. Valid provisions stay far under it.
const maxReplicationGroupNodes = 500

// memberClusters synthesizes the cache cluster ids that make up a replication
// group, matching the "<id>-001", "<id>-002", … naming real ElastiCache assigns.
func memberClusters(id string, nodes int) []string {
	if nodes < 0 {
		nodes = 0
	}

	// Defensive clamp: the count originates from caller input (NumCacheNodes), so
	// bound it before it sizes the allocation regardless of the call path.
	if nodes > maxReplicationGroupNodes {
		nodes = maxReplicationGroupNodes
	}

	members := make([]string, 0, nodes)
	for i := 1; i <= nodes; i++ {
		members = append(members, fmt.Sprintf("%s-%03d", id, i))
	}

	return members
}

// failoverStatus maps the requested AutomaticFailoverEnabled flag to the
// "enabled"/"disabled" status ElastiCache reports on Describe.
func failoverStatus(enabled bool) string {
	if enabled {
		return "enabled"
	}

	return "disabled"
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
		rg.MemberClusters = memberClusters(id, numCacheNodes)
	}

	m.replicationGroups.Set(id, rg)

	return &rg, nil
}

// DeleteReplicationGroup deletes a replication group. When
// opts.FinalSnapshotIdentifier is set it first takes a final snapshot of the
// group (which then shows up in DescribeSnapshots, as real ElastiCache does).
// When opts.RetainPrimaryCluster is set the primary node group is kept as a
// standalone cache cluster instead of being torn down.
func (m *Mock) DeleteReplicationGroup(
	ctx context.Context, id string, opts cachedriver.DeleteReplicationGroupOptions,
) error {
	rg, ok := m.replicationGroups.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound,
			"ReplicationGroupNotFoundFault: replication group %q not found", id)
	}

	if opts.FinalSnapshotIdentifier != "" {
		if _, err := m.CreateSnapshot(ctx, cachedriver.SnapshotConfig{
			SnapshotName:       opts.FinalSnapshotIdentifier,
			ReplicationGroupID: id,
		}); err != nil {
			return err
		}
	}

	if opts.RetainPrimaryCluster {
		m.retainPrimaryCluster(&rg)
		m.replicationGroups.Delete(id)

		return nil
	}

	info := cachedriver.CacheInfo{Name: rg.ID, Engine: rg.Engine}
	if err := cacheengine.Deprovision(ctx, m.opts.CacheEngine, &info); err != nil {
		return err
	}

	m.replicationGroups.Delete(id)

	return nil
}

// retainPrimaryCluster keeps the primary node group of a deleted replication
// group as a standalone cache cluster, so DescribeCacheClusters still returns
// it. The backing engine (if any) is handed over untouched.
func (m *Mock) retainPrimaryCluster(rg *cachedriver.ReplicationGroup) {
	if m.caches.Has(rg.ID) {
		return
	}

	info := cachedriver.CacheInfo{
		Name:            rg.ID,
		NodeType:        rg.NodeType,
		Engine:          rg.Engine,
		EngineVersion:   rg.EngineVersion,
		Status:          statusAvailable,
		Endpoint:        net.JoinHostPort(rg.PrimaryAddress, strconv.Itoa(rg.PrimaryPort)),
		ARN:             m.cacheARN(rg.ID),
		CreatedAt:       m.opts.Clock.Now().UTC().Format(time.RFC3339),
		NumCacheNodes:   1,
		SubnetGroupName: rg.SubnetGroupName,
	}

	m.caches.Set(rg.ID, &cacheData{info: info, items: memstore.New[cacheItem]()})
}
