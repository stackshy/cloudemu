package memorydb

import (
	"context"
	"fmt"
	"net"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cacheengine "github.com/stackshy/cloudemu/v2/services/cache/cacheengine"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
)

// engineRedisFamily is the engine name handed to the shared cache-engine helper.
// MemoryDB is Redis/Valkey-compatible (RESP), so it always reuses the Redis
// CacheEngine regardless of the cluster's own engine field.
const engineRedisFamily = "redis"

// cloneCluster deep-copies slice/map fields so a returned value never aliases
// the store (copy-on-read).
func cloneCluster(in *mdbdriver.Cluster) mdbdriver.Cluster {
	c := *in
	c.Tags = copyTags(c.Tags)
	c.SecurityGroups = append([]mdbdriver.SecurityGroupMembership(nil), c.SecurityGroups...)

	shards := make([]mdbdriver.Shard, len(c.Shards))

	for i := range c.Shards {
		s := c.Shards[i]
		s.Nodes = append([]mdbdriver.Node(nil), c.Shards[i].Nodes...)
		shards[i] = s
	}

	c.Shards = shards

	return c
}

// MemoryDB service limits on cluster topology. Enforcing them before the mock
// allocates the shard/node slices keeps a caller-supplied count from driving
// unbounded memory use.
const (
	maxShardsPerCluster = 500
	maxReplicasPerShard = 5
	// minNodesForFailover is a primary plus at least one replica.
	minNodesForFailover = 2
)

// validateShardTopology bounds caller-supplied shard/replica counts to the
// MemoryDB service limits, rejecting out-of-range requests before allocation.
func validateShardTopology(numShards, replicasPerShard int) error {
	if numShards < 0 || numShards > maxShardsPerCluster {
		return cerrors.Newf(cerrors.InvalidArgument, "NumShards must be between 1 and %d", maxShardsPerCluster)
	}

	if replicasPerShard < 0 || replicasPerShard > maxReplicasPerShard {
		return cerrors.Newf(cerrors.InvalidArgument, "ReplicasPerShard must be between 0 and %d", maxReplicasPerShard)
	}

	return nil
}

// buildShards constructs the shard/node topology + endpoints for a cluster.
// Callers must validate numShards/replicasPerShard via validateShardTopology
// first; the values are bounded to the MemoryDB service limits.
func (m *Mock) buildShards(clusterName string, numShards, replicasPerShard int) []mdbdriver.Shard {
	if numShards <= 0 {
		numShards = 1
	}

	// Defensive clamp to the service limits. Callers reject out-of-range counts
	// via validateShardTopology first, so this never triggers for valid input;
	// it keeps the allocations below bounded regardless of the call path.
	if numShards > maxShardsPerCluster {
		numShards = maxShardsPerCluster
	}

	if replicasPerShard < 0 {
		replicasPerShard = 0
	}

	if replicasPerShard > maxReplicasPerShard {
		replicasPerShard = maxReplicasPerShard
	}

	nodesPerShard := replicasPerShard + 1 // primary + replicas
	now := m.opts.Clock.Now().UTC()

	// Capacities are intentionally left unhinted: the counts, though clamped to
	// the service limits above, originate from caller input, and a tainted
	// make() size is an uncontrolled-allocation risk. The clamped loop bounds
	// keep the append-driven growth bounded.
	var shards []mdbdriver.Shard

	for s := 1; s <= numShards; s++ {
		shardName := fmt.Sprintf("%04d", s)

		var nodes []mdbdriver.Node

		for n := 1; n <= nodesPerShard; n++ {
			nodeName := fmt.Sprintf("%s-%s-%03d", clusterName, shardName, n)
			nodes = append(nodes, mdbdriver.Node{
				Name:             nodeName,
				Status:           mdbdriver.StatusAvailable,
				AvailabilityZone: m.opts.Region + azSuffix(n),
				CreateTime:       now,
				Endpoint: mdbdriver.Endpoint{
					Address: fmt.Sprintf("%s.%s.memorydb.%s.amazonaws.com", nodeName, clusterName, m.opts.Region),
					Port:    defaultPort,
				},
			})
		}

		shards = append(shards, mdbdriver.Shard{
			Name:          shardName,
			Status:        mdbdriver.StatusAvailable,
			Slots:         slotRange(s, numShards),
			NumberOfNodes: nodesPerShard,
			Nodes:         nodes,
		})
	}

	return shards
}

func azSuffix(n int) string {
	return string(rune('a' + ((n - 1) % 3)))
}

// slotRange splits the 16384 Redis slots evenly across shards.
func slotRange(shard, total int) string {
	const slots = 16384
	per := slots / total
	start := (shard - 1) * per
	end := start + per - 1

	if shard == total {
		end = slots - 1
	}

	return fmt.Sprintf("%d-%d", start, end)
}

// validateClusterRefs checks that referenced ACL/parameter-group/subnet-group
// exist. A dangling reference is an invalid input to the create/update call
// (not a missing cluster), so it surfaces as InvalidArgument — real MemoryDB
// answers CreateCluster with a bad ACLName as InvalidParameterValueException.
// The caller holds the lock.
func (m *Mock) validateClusterRefs(aclName, paramGroup, subnetGroup string) error {
	if aclName != "" && !m.acls.Has(aclName) {
		return cerrors.Newf(cerrors.InvalidArgument, "ACL %q not found", aclName)
	}

	if paramGroup != "" && !m.parameterGroups.Has(paramGroup) {
		return cerrors.Newf(cerrors.InvalidArgument, "parameter group %q not found", paramGroup)
	}

	if subnetGroup != "" && !m.subnetGroups.Has(subnetGroup) {
		return cerrors.Newf(cerrors.InvalidArgument, "subnet group %q not found", subnetGroup)
	}

	return nil
}

// validateMultiRegionRef rejects a create request naming a multi-region cluster
// that does not exist. The caller holds the lock.
func (m *Mock) validateMultiRegionRef(name string) error {
	if name != "" && !m.multiRegion.Has(name) {
		return cerrors.Newf(cerrors.InvalidArgument, "multi-region cluster %q not found", name)
	}

	return nil
}

func buildSecurityGroups(ids []string) []mdbdriver.SecurityGroupMembership {
	sgs := make([]mdbdriver.SecurityGroupMembership, 0, len(ids))
	for _, id := range ids {
		sgs = append(sgs, mdbdriver.SecurityGroupMembership{SecurityGroupID: id, Status: mdbdriver.StatusAvailable})
	}

	return sgs
}

// clusterShape holds the resolvable cluster dimensions that a restore may fill
// in from the source snapshot when the caller leaves them unset.
type clusterShape struct {
	numShards   int
	replicas    int
	nodeType    string
	subnetGroup string
	pg          string
	tls         bool
}

// applySnapshotShape fills any unset shape field from the named snapshot's
// stored configuration. Referenced subnet/parameter groups are honored only if
// they still exist. The caller holds the lock.
func (m *Mock) applySnapshotShape(cfg *mdbdriver.CreateClusterConfig, shape *clusterShape) error {
	snap, ok := m.snapshots.Get(cfg.SnapshotName)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "snapshot %q not found", cfg.SnapshotName)
	}

	sc := snap.ClusterConfiguration
	if cfg.NumShards == 0 {
		shape.numShards = sc.NumShards
	}

	if cfg.NumReplicasPerShard == 0 {
		shape.replicas = sc.ReplicasPerShard
	}

	if cfg.NodeType == "" {
		shape.nodeType = orDefault(sc.NodeType, shape.nodeType)
	}

	if cfg.SubnetGroupName == "" && m.subnetGroups.Has(sc.SubnetGroupName) {
		shape.subnetGroup = sc.SubnetGroupName
	}

	if cfg.ParameterGroupName == "" && m.parameterGroups.Has(sc.ParameterGroupName) {
		shape.pg = sc.ParameterGroupName
	}

	if !cfg.TLSEnabled {
		shape.tls = sc.TLSEnabled
	}

	return nil
}

// CreateCluster creates a MemoryDB cluster. The cluster row is reserved (built
// with its synthetic endpoint and stored) under the provider lock, then the real
// Redis provision runs WITHOUT the lock so concurrent reads are never blocked,
// and only the resulting reachable host:port is written back under the lock.
//
//nolint:gocritic // cfg is large but matches the driver signature.
func (m *Mock) CreateCluster(ctx context.Context, cfg mdbdriver.CreateClusterConfig) (*mdbdriver.Cluster, error) {
	if err := validName("cluster", cfg.Name); err != nil {
		return nil, err
	}

	reserved, err := m.reserveCluster(&cfg)
	if err != nil {
		return nil, err
	}

	// No engine wired in: the synthetic cluster is complete as reserved.
	if m.opts.CacheEngine == nil {
		return reserved, nil
	}

	host, port, err := m.provisionClusterEngine(ctx, reserved.Name, reserved.ClusterEndpoint)
	if err != nil {
		m.rollbackCluster(reserved.Name, reserved.ACLName, cfg.MultiRegionClusterName)

		return nil, err
	}

	out := m.finalizeClusterEngine(reserved, host, port)

	return &out, nil
}

// reserveCluster validates cfg, builds the cluster with its synthetic endpoint,
// stores it, links its ACL and multi-region membership, and emits the create
// metrics/event — all under the provider lock. It returns a clone of the stored
// cluster.
func (m *Mock) reserveCluster(cfg *mdbdriver.CreateClusterConfig) (*mdbdriver.Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.clusters.Has(cfg.Name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "cluster %q already exists", cfg.Name)
	}

	if err := m.validateClusterRefs(cfg.ACLName, cfg.ParameterGroupName, cfg.SubnetGroupName); err != nil {
		return nil, err
	}

	shape := clusterShape{
		numShards:   cfg.NumShards,
		replicas:    cfg.NumReplicasPerShard,
		nodeType:    orDefault(cfg.NodeType, defaultNodeType),
		subnetGroup: cfg.SubnetGroupName,
		pg:          orDefault(cfg.ParameterGroupName, "default.memorydb-redis7"),
		tls:         cfg.TLSEnabled,
	}
	engine := orDefault(cfg.Engine, defaultEngine)
	engineVersion := orDefault(cfg.EngineVersion, defaultEngineVersion)
	acl := orDefault(cfg.ACLName, "open-access")

	if cfg.SnapshotName != "" {
		if err := m.applySnapshotShape(cfg, &shape); err != nil {
			return nil, err
		}
	}

	if err := validateShardTopology(shape.numShards, shape.replicas); err != nil {
		return nil, err
	}

	if err := m.validateMultiRegionRef(cfg.MultiRegionClusterName); err != nil {
		return nil, err
	}

	sgs := buildSecurityGroups(cfg.SecurityGroupIDs)

	cluster := mdbdriver.Cluster{
		Name:                 cfg.Name,
		ARN:                  m.arn("cluster", cfg.Name),
		Description:          cfg.Description,
		Status:               mdbdriver.StatusAvailable,
		NodeType:             shape.nodeType,
		Engine:               engine,
		EngineVersion:        engineVersion,
		EnginePatchVersion:   engineVersion + ".0",
		NumberOfShards:       maxInt(shape.numShards, 1),
		ACLName:              acl,
		ParameterGroupName:   shape.pg,
		ParameterGroupStatus: mdbdriver.StatusAvailable,
		SubnetGroupName:      shape.subnetGroup,
		SecurityGroups:       sgs,
		Shards:               m.buildShards(cfg.Name, shape.numShards, shape.replicas),
		ClusterEndpoint: mdbdriver.Endpoint{
			Address: fmt.Sprintf("clustercfg.%s.memorydb.%s.amazonaws.com", cfg.Name, m.opts.Region),
			Port:    portOr(cfg.Port),
		},
		TLSEnabled:              shape.tls,
		KmsKeyID:                cfg.KmsKeyID,
		MaintenanceWindow:       orDefault(cfg.MaintenanceWindow, "sun:23:00-mon:01:30"),
		SnapshotWindow:          orDefault(cfg.SnapshotWindow, "05:00-06:00"),
		SnapshotRetentionLimit:  cfg.SnapshotRetentionLimit,
		SnsTopicARN:             cfg.SnsTopicARN,
		AutoMinorVersionUpgrade: cfg.AutoMinorVersionUpgrade,
		DataTiering:             cfg.DataTiering,
		AvailabilityMode:        availabilityMode(shape.replicas),
		NetworkType:             orDefault(cfg.NetworkType, "ipv4"),
		IPDiscovery:             orDefault(cfg.IPDiscovery, "ipv4"),
		MultiRegionClusterName:  cfg.MultiRegionClusterName,
		Tags:                    copyTags(cfg.Tags),
		CreatedAt:               m.opts.Clock.Now().UTC(),
	}

	m.clusters.Set(cfg.Name, cluster)

	// Create-time tags must be addressable by ListTags(arn), which reads m.tags;
	// without this they would live only on cluster.Tags and be invisible until a
	// later TagResource call.
	if len(cfg.Tags) > 0 {
		m.tags[cluster.ARN] = copyTags(cfg.Tags)
	}

	m.linkACLCluster(acl, cfg.Name, true)

	if cfg.MultiRegionClusterName != "" {
		m.registerMRCMember(cfg.MultiRegionClusterName, cfg.Name, cluster.ARN)
	}

	m.emitClusterMetrics(cfg.Name)
	m.recordClusterEvent(cfg.Name, "Cluster created")

	out := cloneCluster(&cluster)

	return &out, nil
}

// provisionClusterEngine starts the real Redis server backing the cluster and
// returns the reachable host:port. It runs WITHOUT the provider lock held; the
// caller invokes it only when a CacheEngine is configured.
func (m *Mock) provisionClusterEngine(
	ctx context.Context, name string, ep mdbdriver.Endpoint,
) (host string, port int, err error) {
	info := cachedriver.CacheInfo{
		Name:     name,
		Engine:   engineRedisFamily,
		Endpoint: fmt.Sprintf("%s:%d", ep.Address, ep.Port),
	}

	if err := cacheengine.Provision(ctx, m.opts.CacheEngine, &info); err != nil {
		return "", 0, err
	}

	return splitHostPort(info.Endpoint)
}

// finalizeClusterEngine writes the engine's reachable host:port back onto the
// reserved cluster's endpoint and shard nodes, persisting the update under a
// re-acquired lock, and returns the finalized cluster.
func (m *Mock) finalizeClusterEngine(reserved *mdbdriver.Cluster, host string, port int) mdbdriver.Cluster {
	reserved.ClusterEndpoint.Address = host
	reserved.ClusterEndpoint.Port = port

	for si := range reserved.Shards {
		for ni := range reserved.Shards[si].Nodes {
			reserved.Shards[si].Nodes[ni].Endpoint.Address = host
			reserved.Shards[si].Nodes[ni].Endpoint.Port = port
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// The row is normally still present; skip the write only if it was removed
	// concurrently, returning the reserved copy unchanged.
	if m.clusters.Has(reserved.Name) {
		m.clusters.Set(reserved.Name, *reserved)
	}

	return cloneCluster(reserved)
}

// rollbackCluster undoes reserveCluster when the engine provision fails: it
// removes the reserved cluster and its ACL / multi-region membership.
func (m *Mock) rollbackCluster(name, acl, mrc string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.clusters.Delete(name)
	m.linkACLCluster(acl, name, false)

	if mrc != "" {
		m.unregisterMRCMember(mrc, name)
	}
}

// DescribeClusters returns all clusters, or the named ones.
func (m *Mock) DescribeClusters(_ context.Context, names []string) ([]mdbdriver.Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeByName(m.clusters, names, cloneCluster, func(n string) error {
		return cerrors.Newf(cerrors.NotFound, "cluster %q not found", n)
	})
}

// UpdateCluster applies the non-zero fields of cfg to an existing cluster.
//
//nolint:gocritic // cfg is large but matches the driver signature.
func (m *Mock) UpdateCluster(_ context.Context, cfg mdbdriver.UpdateClusterConfig) (*mdbdriver.Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.clusters.Get(cfg.Name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", cfg.Name)
	}

	if cfg.ACLName != "" {
		if !m.acls.Has(cfg.ACLName) {
			return nil, cerrors.Newf(cerrors.InvalidArgument, "ACL %q not found", cfg.ACLName)
		}

		m.linkACLCluster(c.ACLName, cfg.Name, false)
		m.linkACLCluster(cfg.ACLName, cfg.Name, true)
		c.ACLName = cfg.ACLName
	}

	if cfg.ParameterGroupName != "" {
		if !m.parameterGroups.Has(cfg.ParameterGroupName) {
			return nil, cerrors.Newf(cerrors.InvalidArgument, "parameter group %q not found", cfg.ParameterGroupName)
		}

		c.ParameterGroupName = cfg.ParameterGroupName
	}

	c.Description = orKeep(cfg.Description, c.Description)
	c.NodeType = orKeep(cfg.NodeType, c.NodeType)
	c.EngineVersion = orKeep(cfg.EngineVersion, c.EngineVersion)
	c.MaintenanceWindow = orKeep(cfg.MaintenanceWindow, c.MaintenanceWindow)
	c.SnapshotWindow = orKeep(cfg.SnapshotWindow, c.SnapshotWindow)
	c.SnsTopicARN = orKeep(cfg.SnsTopicARN, c.SnsTopicARN)

	if cfg.SnapshotRetentionLimit != nil {
		c.SnapshotRetentionLimit = *cfg.SnapshotRetentionLimit
	}

	if err := m.reconfigureShards(&c, &cfg); err != nil {
		return nil, err
	}

	m.clusters.Set(cfg.Name, c)
	m.recordClusterEvent(cfg.Name, "Cluster modified")

	out := cloneCluster(&c)

	return &out, nil
}

// reconfigureShards rebuilds the shard/node topology for an in-place shard-count
// or replica-count change, enforcing the MemoryDB service limits before any
// allocation. The caller holds the lock.
func (m *Mock) reconfigureShards(c *mdbdriver.Cluster, cfg *mdbdriver.UpdateClusterConfig) error {
	if cfg.ShardCount != nil && *cfg.ShardCount > 0 {
		if err := validateShardTopology(*cfg.ShardCount, len(c.Shards[0].Nodes)-1); err != nil {
			return err
		}

		c.NumberOfShards = *cfg.ShardCount
		c.Shards = m.buildShards(c.Name, *cfg.ShardCount, len(c.Shards[0].Nodes)-1)
	}

	if cfg.ReplicaCount != nil {
		if err := validateShardTopology(c.NumberOfShards, *cfg.ReplicaCount); err != nil {
			return err
		}

		c.Shards = m.buildShards(c.Name, c.NumberOfShards, *cfg.ReplicaCount)
		c.AvailabilityMode = availabilityMode(*cfg.ReplicaCount)
	}

	return nil
}

// DeleteCluster removes a cluster (optionally taking a final snapshot), tearing
// down the real Redis backing it. The engine Deprovision runs WITHOUT the
// provider lock held.
func (m *Mock) DeleteCluster(ctx context.Context, name, finalSnapshotName string) (*mdbdriver.Cluster, error) {
	// No engine wired in: complete the delete under a single lock, as before.
	if m.opts.CacheEngine == nil {
		m.mu.Lock()
		defer m.mu.Unlock()

		c, err := m.stageClusterDeleteLocked(name, finalSnapshotName)
		if err != nil {
			return nil, err
		}

		return m.removeClusterLocked(&c), nil
	}

	// Capture the cluster (and take the optional final snapshot) under the lock,
	// deprovision the real Redis WITHOUT it, then remove the row under the lock.
	m.mu.Lock()
	c, err := m.stageClusterDeleteLocked(name, finalSnapshotName)
	m.mu.Unlock()

	if err != nil {
		return nil, err
	}

	if err := cacheengine.Deprovision(ctx, m.opts.CacheEngine, &cachedriver.CacheInfo{
		Name:   name,
		Engine: engineRedisFamily,
	}); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.removeClusterLocked(&c), nil
}

// stageClusterDeleteLocked verifies the cluster exists and takes the optional
// final snapshot, returning the cluster to be removed. The caller holds the lock.
func (m *Mock) stageClusterDeleteLocked(name, finalSnapshotName string) (mdbdriver.Cluster, error) {
	c, ok := m.clusters.Get(name)
	if !ok {
		return mdbdriver.Cluster{}, cerrors.Newf(cerrors.NotFound, "cluster %q not found", name)
	}

	if finalSnapshotName != "" {
		if m.snapshots.Has(finalSnapshotName) {
			return mdbdriver.Cluster{}, cerrors.Newf(cerrors.AlreadyExists, "snapshot %q already exists", finalSnapshotName)
		}

		m.snapshots.Set(finalSnapshotName, m.snapshotFromCluster(finalSnapshotName, &c, "manual", nil))
	}

	return c, nil
}

// removeClusterLocked deletes the cluster row and its ACL / multi-region links,
// records the delete event, and returns the deleting-state cluster. The caller
// holds the lock.
func (m *Mock) removeClusterLocked(c *mdbdriver.Cluster) *mdbdriver.Cluster {
	m.clusters.Delete(c.Name)
	m.linkACLCluster(c.ACLName, c.Name, false)

	if c.MultiRegionClusterName != "" {
		m.unregisterMRCMember(c.MultiRegionClusterName, c.Name)
	}

	m.recordClusterEvent(c.Name, "Cluster deleted")

	c.Status = mdbdriver.StatusDeleting
	out := cloneCluster(c)

	return &out
}

// FailoverShard triggers a failover of a shard's primary; the shard stays
// available.
func (m *Mock) FailoverShard(_ context.Context, clusterName, shardName string) (*mdbdriver.Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.clusters.Get(clusterName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", clusterName)
	}

	shard := -1

	for i := range c.Shards {
		if c.Shards[i].Name == shardName {
			shard = i
			break
		}
	}

	if shard < 0 {
		return nil, cerrors.Newf(cerrors.NotFound, "shard %q not found in cluster %q", shardName, clusterName)
	}

	if c.Shards[shard].NumberOfNodes < minNodesForFailover {
		return nil, cerrors.New(cerrors.FailedPrecondition, "failover requires at least one replica in the shard")
	}

	m.recordClusterEvent(clusterName, "Failover started for shard "+shardName)

	out := cloneCluster(&c)

	return &out, nil
}

// ListAllowedNodeTypeUpdates returns the scale-up/scale-down node types.
func (m *Mock) ListAllowedNodeTypeUpdates(_ context.Context, clusterName string) (scaleUp, scaleDown []string, err error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.clusters.Has(clusterName) {
		return nil, nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", clusterName)
	}

	return []string{"db.r7g.large", "db.r7g.xlarge"}, []string{"db.t4g.medium"}, nil
}

// splitHostPort splits a "host:port" endpoint into its parts.
func splitHostPort(endpoint string) (host string, port int, err error) {
	host, portStr, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", 0, cerrors.Newf(cerrors.Internal, "parse cache engine endpoint %q: %v", endpoint, err)
	}

	port, err = strconv.Atoi(portStr)
	if err != nil {
		return "", 0, cerrors.Newf(cerrors.Internal, "parse cache engine port %q: %v", portStr, err)
	}

	return host, port, nil
}

// ---- small helpers ----

func orDefault(v, def string) string {
	if v == "" {
		return def
	}

	return v
}

func orKeep(v, cur string) string {
	if v == "" {
		return cur
	}

	return v
}

func portOr(p int) int {
	if p == 0 {
		return defaultPort
	}

	return p
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}

	return b
}

func availabilityMode(replicas int) string {
	if replicas > 0 {
		return "MultiAZ"
	}

	return "SingleAZ"
}
