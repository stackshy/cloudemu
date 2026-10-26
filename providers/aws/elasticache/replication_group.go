package elasticache

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/regionctx"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	cacheengine "github.com/stackshy/cloudemu/v2/services/cache/cacheengine"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
)

// CreateReplicationGroup creates a replication group.
//
// A duplicate id reports ReplicationGroupAlreadyExists: callers re-running a
// provision treat that specific code as "already there, carry on", so folding
// it into a generic error would turn a safe re-run into a hard failure.
//
// The id is reserved under mu while the request is validated, the optional real
// engine is provisioned with no lock held, and the group is committed under mu.
func (m *Mock) CreateReplicationGroup(
	ctx context.Context, cfg cachedriver.ReplicationGroupConfig,
) (*cachedriver.ReplicationGroup, error) {
	rg, err := m.reserveReplicationGroup(ctx, &cfg)
	if err != nil {
		return nil, err
	}

	// Opt-in: back the primary with a real Redis server, replacing the synthetic
	// hostname with the host:port a client connects to. Routes through the same
	// cacheengine helper the cache-cluster path uses so the two cannot drift.
	if err = m.provisionPrimaryEndpoint(ctx, &rg); err != nil {
		m.mu.Lock()
		delete(m.rgRes, rg.ID)
		m.mu.Unlock()

		return nil, err
	}

	return m.commitReplicationGroup(&rg), nil
}

// reserveReplicationGroup validates a create under mu, builds the group, and
// reserves its id in the creating state.
func (m *Mock) reserveReplicationGroup(
	ctx context.Context, cfg *cachedriver.ReplicationGroupConfig,
) (cachedriver.ReplicationGroup, error) {
	if cfg.ID == "" {
		return cachedriver.ReplicationGroup{}, cerrors.New(cerrors.InvalidArgument, "ReplicationGroupId is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, reserved := m.rgRes[cfg.ID]; reserved || m.replicationGroups.Has(cfg.ID) {
		return cachedriver.ReplicationGroup{}, rgAlreadyExists(cfg.ID)
	}

	rg, err := m.buildReplicationGroup(ctx, cfg)
	if err != nil {
		return cachedriver.ReplicationGroup{}, err
	}

	// Each member is a describable cache cluster, so a member id already used by
	// a cluster (stored or reserved) is a CacheClusterAlreadyExists, as in AWS.
	for _, member := range rg.MemberClusters {
		if _, reserved := m.cacheRes[member]; reserved || m.caches.Has(member) {
			return cachedriver.ReplicationGroup{}, cacheAlreadyExists(member)
		}
	}

	m.rgRes[cfg.ID] = &reservation[cachedriver.ReplicationGroup]{state: statusCreating, record: rg}

	return rg, nil
}

// commitReplicationGroup stores a provisioned group and drops its reservation.
func (m *Mock) commitReplicationGroup(rg *cachedriver.ReplicationGroup) *cachedriver.ReplicationGroup {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.rgRes, rg.ID)
	m.replicationGroups.Set(rg.ID, *rg)

	// Under AsyncSettle a fresh group reports creating until the window elapses
	// (CreateReplicationGroup → creating → available); a no-op when settle is off.
	m.rgSettle.Begin(rg.ID, statusCreating, m.opts.Clock.Now(),
		m.opts.SettleDuration(settle.DefaultCacheSettle))

	result := cloneReplicationGroup(rg)
	result.Status = m.settleRGStatus(rg.ID, result.Status)

	return &result
}

// cloneReplicationGroup copies a group so callers cannot alias its stored
// member list.
func cloneReplicationGroup(rg *cachedriver.ReplicationGroup) cachedriver.ReplicationGroup {
	out := *rg
	out.MemberClusters = append([]string(nil), rg.MemberClusters...)

	return out
}

// buildReplicationGroup applies the restore seed and defaults to a create
// request and validates it against its parents. The caller holds mu.
func (m *Mock) buildReplicationGroup(
	ctx context.Context, cfg *cachedriver.ReplicationGroupConfig,
) (cachedriver.ReplicationGroup, error) {
	// A restore (SnapshotName set) seeds the unset config fields from the
	// snapshot before defaults are applied.
	if err := m.seedReplicationGroupRestore(cfg); err != nil {
		return cachedriver.ReplicationGroup{}, err
	}

	engine := cfg.Engine
	if engine == "" {
		engine = defaultEngine
	}

	if err := validateEngine(engine, true); err != nil {
		return cachedriver.ReplicationGroup{}, err
	}

	if err := validateReplicationNodeCount(cfg.NumCacheNodes); err != nil {
		return cachedriver.ReplicationGroup{}, err
	}

	if err := m.requireParameterGroup(cfg.ParameterGroupName); err != nil {
		return cachedriver.ReplicationGroup{}, err
	}

	nodeType := cfg.NodeType
	if nodeType == "" {
		nodeType = defaultNodeType
	}

	nodes := max(cfg.NumCacheNodes, 1)

	engineVersion := cfg.EngineVersion
	if engineVersion == "" {
		engineVersion = defaultEngineVersion(engine)
	}

	region := regionctx.RegionOr(ctx, m.opts.Region)

	return cachedriver.ReplicationGroup{
		ID:            cfg.ID,
		Description:   cfg.Description,
		Status:        statusAvailable,
		Engine:        engine,
		EngineVersion: engineVersion,
		NodeType:      nodeType,
		NumCacheNodes: nodes,
		// Callers read the primary endpoint to build a connection string; a
		// group without one is indistinguishable from a broken provision.
		PrimaryAddress: fmt.Sprintf("%s.%s.cache.amazonaws.com", cfg.ID, region),
		PrimaryPort:    defaultRedisPort,
		// The reader endpoint lets clients scale reads across the replicas.
		ReaderAddress:      fmt.Sprintf("%s-ro.%s.cache.amazonaws.com", cfg.ID, region),
		ReaderPort:         defaultRedisPort,
		MemberClusters:     memberClusters(cfg.ID, nodes),
		AutomaticFailover:  failoverStatus(cfg.AutomaticFailoverEnabled),
		SubnetGroupName:    cfg.SubnetGroupName,
		ParameterGroupName: cfg.ParameterGroupName,
		ARN:                "arn:aws:elasticache:" + region + ":" + m.opts.AccountID + ":replicationgroup:" + cfg.ID,
	}, nil
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
	// bound it with an explicit comparison immediately before the allocation it
	// sizes, regardless of the call path. Valid provisions stay far under the
	// ceiling, so this is a no-op for them.
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
// A group whose real engine is still being provisioned or torn down is reported
// with status creating or deleting.
func (m *Mock) DescribeReplicationGroups(
	_ context.Context, ids []string,
) ([]cachedriver.ReplicationGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(ids) == 0 {
		stored := m.replicationGroups.SortedValues()
		out := make([]cachedriver.ReplicationGroup, 0, len(stored)+len(m.rgRes))

		for i := range stored {
			rg := cloneReplicationGroup(&stored[i])
			rg.Status = m.settleRGStatus(rg.ID, rg.Status)
			out = append(out, rg)
		}

		for _, r := range m.rgRes {
			rg := cloneReplicationGroup(&r.record)
			rg.Status = r.state
			out = append(out, rg)
		}

		slices.SortFunc(out, func(a, b cachedriver.ReplicationGroup) int { return strings.Compare(a.ID, b.ID) })

		return out, nil
	}

	out := make([]cachedriver.ReplicationGroup, 0, len(ids))

	for _, id := range ids {
		rg, err := m.describeReplicationGroup(id)
		if err != nil {
			return nil, err
		}

		out = append(out, rg)
	}

	return out, nil
}

// describeReplicationGroup reads one group, falling back to a pending
// reservation. The caller holds mu.
func (m *Mock) describeReplicationGroup(id string) (cachedriver.ReplicationGroup, error) {
	if rg, ok := m.replicationGroups.Get(id); ok {
		out := cloneReplicationGroup(&rg)
		out.Status = m.settleRGStatus(id, out.Status)

		return out, nil
	}

	if r, reserved := m.rgRes[id]; reserved {
		out := cloneReplicationGroup(&r.record)
		out.Status = r.state

		return out, nil
	}

	return cachedriver.ReplicationGroup{}, rgNotFound(id)
}

// mutableReplicationGroup returns the stored group a mutating call targets. A
// group that is still being created or is being deleted reports
// InvalidReplicationGroupState, not ReplicationGroupNotFoundFault. The caller
// holds mu.
func (m *Mock) mutableReplicationGroup(id string) (cachedriver.ReplicationGroup, error) {
	if rg, ok := m.replicationGroups.Get(id); ok {
		return rg, nil
	}

	if r, reserved := m.rgRes[id]; reserved {
		return cachedriver.ReplicationGroup{}, rgInvalidState(id, r.state)
	}

	return cachedriver.ReplicationGroup{}, rgNotFound(id)
}

// ModifyReplicationGroup changes the node count of an existing group.
func (m *Mock) ModifyReplicationGroup(
	_ context.Context, id string, numCacheNodes int,
) (*cachedriver.ReplicationGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rg, err := m.mutableReplicationGroup(id)
	if err != nil {
		return nil, err
	}

	if err := validateReplicationNodeCount(numCacheNodes); err != nil {
		return nil, err
	}

	if numCacheNodes > 0 {
		rg.NumCacheNodes = numCacheNodes
		rg.MemberClusters = memberClusters(id, numCacheNodes)
	}

	m.replicationGroups.Set(id, rg)

	// Under AsyncSettle a modified group briefly reports modifying before settling
	// back to available; a no-op when settle is off.
	m.rgSettle.Begin(id, statusModifying, m.opts.Clock.Now(),
		m.opts.SettleDuration(settle.DefaultCacheModifySettle))

	result := cloneReplicationGroup(&rg)
	result.Status = m.settleRGStatus(id, result.Status)

	return &result, nil
}

// ModifyReplicationGroupParameterGroup points a replication group at another
// cache parameter group, which must exist. AWS-only; the wire handler reaches it
// by type assertion.
func (m *Mock) ModifyReplicationGroupParameterGroup(
	_ context.Context, id, name string,
) (*cachedriver.ReplicationGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rg, err := m.mutableReplicationGroup(id)
	if err != nil {
		return nil, err
	}

	if err := m.requireParameterGroup(name); err != nil {
		return nil, err
	}

	rg.ParameterGroupName = name
	m.replicationGroups.Set(id, rg)

	result := cloneReplicationGroup(&rg)
	result.Status = m.settleRGStatus(id, result.Status)

	return &result, nil
}

// DeleteReplicationGroup deletes a replication group. When
// opts.FinalSnapshotIdentifier is set it first takes a final snapshot of the
// group (which then shows up in DescribeSnapshots, as real ElastiCache does).
// When opts.RetainPrimaryCluster is set the primary node group is kept as a
// standalone cache cluster instead of being torn down.
//
// Otherwise the group is removed under mu and its id is held in the deleting
// state until the real engine (if any) has been torn down. If the teardown
// fails the group is put back, as it was never deleted.
func (m *Mock) DeleteReplicationGroup(
	ctx context.Context, id string, opts cachedriver.DeleteReplicationGroupOptions,
) error {
	m.mu.Lock()

	rg, err := m.mutableReplicationGroup(id)
	if err != nil {
		m.mu.Unlock()

		return err
	}

	if opts.FinalSnapshotIdentifier != "" {
		if _, err := m.createSnapshotLocked(ctx, cachedriver.SnapshotConfig{
			SnapshotName:       opts.FinalSnapshotIdentifier,
			ReplicationGroupID: id,
		}); err != nil {
			m.mu.Unlock()

			return err
		}
	}

	m.replicationGroups.Delete(id)
	m.rgSettle.Clear(id)

	if opts.RetainPrimaryCluster {
		m.retainPrimaryCluster(&rg)
		m.mu.Unlock()

		return nil
	}

	last := cloneReplicationGroup(&rg)
	last.Status = statusDeleting
	m.rgRes[id] = &reservation[cachedriver.ReplicationGroup]{state: statusDeleting, record: last}
	m.mu.Unlock()

	info := cachedriver.CacheInfo{Name: rg.ID, Engine: rg.Engine}
	derr := cacheengine.Deprovision(ctx, m.opts.CacheEngine, &info)

	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.rgRes, id)

	if derr != nil {
		m.replicationGroups.Set(id, rg)

		return derr
	}

	return nil
}

// retainPrimaryCluster keeps the primary node group of a deleted replication
// group as a standalone cache cluster, so DescribeCacheClusters still returns
// it. The backing engine (if any) is handed over untouched. The caller holds mu.
func (m *Mock) retainPrimaryCluster(rg *cachedriver.ReplicationGroup) {
	if m.caches.Has(rg.ID) || m.cacheRes[rg.ID] != nil {
		return
	}

	info := cachedriver.CacheInfo{
		Name:                    rg.ID,
		NodeType:                rg.NodeType,
		Engine:                  rg.Engine,
		EngineVersion:           rg.EngineVersion,
		Status:                  statusAvailable,
		Endpoint:                net.JoinHostPort(rg.PrimaryAddress, strconv.Itoa(rg.PrimaryPort)),
		ARN:                     m.cacheARN(arnRegion(rg.ARN, m.opts.Region), rg.ID),
		CreatedAt:               m.opts.Clock.Now().UTC().Format(time.RFC3339),
		NumCacheNodes:           1,
		SubnetGroupName:         rg.SubnetGroupName,
		AutoMinorVersionUpgrade: true,
	}

	m.caches.Set(rg.ID, &cacheData{info: info, items: memstore.New[cacheItem]()})
}
