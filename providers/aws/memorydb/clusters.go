package memorydb

import (
	"context"
	"fmt"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
)

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

// CreateCluster creates a MemoryDB cluster.
//
//nolint:gocritic // cfg is large but matches the driver signature.
func (m *Mock) CreateCluster(_ context.Context, cfg mdbdriver.CreateClusterConfig) (*mdbdriver.Cluster, error) {
	if err := validName("cluster", cfg.Name); err != nil {
		return nil, err
	}

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
		if err := m.applySnapshotShape(&cfg, &shape); err != nil {
			return nil, err
		}
	}

	if err := validateShardTopology(shape.numShards, shape.replicas); err != nil {
		return nil, err
	}

	if cfg.MultiRegionClusterName != "" && !m.multiRegion.Has(cfg.MultiRegionClusterName) {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"multi-region cluster %q not found", cfg.MultiRegionClusterName)
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
	m.linkACLCluster(acl, cfg.Name, true)

	if cfg.MultiRegionClusterName != "" {
		m.registerMRCMember(cfg.MultiRegionClusterName, cfg.Name, cluster.ARN)
	}

	m.emitClusterMetrics(cfg.Name)
	m.recordClusterEvent(cfg.Name, "Cluster created")

	out := cloneCluster(&cluster)

	return &out, nil
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

// DeleteCluster removes a cluster (optionally taking a final snapshot).
func (m *Mock) DeleteCluster(_ context.Context, name, finalSnapshotName string) (*mdbdriver.Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.clusters.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", name)
	}

	if finalSnapshotName != "" {
		if m.snapshots.Has(finalSnapshotName) {
			return nil, cerrors.Newf(cerrors.AlreadyExists, "snapshot %q already exists", finalSnapshotName)
		}

		m.snapshots.Set(finalSnapshotName, m.snapshotFromCluster(finalSnapshotName, &c, "manual", nil))
	}

	m.clusters.Delete(name)
	m.linkACLCluster(c.ACLName, name, false)

	if c.MultiRegionClusterName != "" {
		m.unregisterMRCMember(c.MultiRegionClusterName, name)
	}

	m.recordClusterEvent(name, "Cluster deleted")

	c.Status = mdbdriver.StatusDeleting
	out := cloneCluster(&c)

	return &out, nil
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
