// Package rds provides an in-memory mock of AWS RDS (and Aurora). It
// implements relationaldb/driver.RelationalDB so the same backend serves both
// the portable API (relationaldb.DB) and the SDK-compat HTTP layer.
//
// The mock manages instance/cluster lifecycle, snapshot/restore, and emits
// CloudWatch-shaped metrics on lifecycle transitions so consumers can assert
// on monitoring side-effects exactly as they would in real AWS.
package rds

import (
	"context"
	"fmt"
	"sync"

	"github.com/stackshy/cloudemu/config"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	rdsdriver "github.com/stackshy/cloudemu/relationaldb/driver"
)

const (
	defaultPort          = 3306
	defaultPortPostgres  = 5432
	defaultStorage       = 20
	defaultStorageType   = "gp2"
	defaultInstanceClass = "db.t3.micro"
	cpuMetricRunning     = 25.0
	connectionsRunning   = 5.0
	cpuMetricStopped     = 0.0
)

var _ rdsdriver.RelationalDB = (*Mock)(nil)

// Mock is the in-memory AWS RDS implementation.
type Mock struct {
	mu sync.RWMutex

	instances        *memstore.Store[rdsdriver.Instance]
	clusters         *memstore.Store[rdsdriver.Cluster]
	snapshots        *memstore.Store[rdsdriver.Snapshot]
	clusterSnapshots *memstore.Store[rdsdriver.ClusterSnapshot]

	opts       *config.Options
	monitoring mondriver.Monitoring
}

// New creates a new AWS RDS mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		instances:        memstore.New[rdsdriver.Instance](),
		clusters:         memstore.New[rdsdriver.Cluster](),
		snapshots:        memstore.New[rdsdriver.Snapshot](),
		clusterSnapshots: memstore.New[rdsdriver.ClusterSnapshot](),
		opts:             opts,
	}
}

// SetMonitoring wires a CloudWatch-style backend for auto-metric emission.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

func (m *Mock) emitInstanceMetrics(instanceID string, cpu, connections float64) {
	if m.monitoring == nil {
		return
	}

	now := m.opts.Clock.Now()
	dims := map[string]string{"DBInstanceIdentifier": instanceID}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{
		{Namespace: "AWS/RDS", MetricName: "CPUUtilization", Value: cpu, Unit: "Percent", Dimensions: dims, Timestamp: now},
		{Namespace: "AWS/RDS", MetricName: "DatabaseConnections", Value: connections, Unit: "Count", Dimensions: dims, Timestamp: now},
		{Namespace: "AWS/RDS", MetricName: "FreeableMemory", Value: 1 << 30, Unit: "Bytes", Dimensions: dims, Timestamp: now},
		{Namespace: "AWS/RDS", MetricName: "ReadIOPS", Value: 10, Unit: "Count/Second", Dimensions: dims, Timestamp: now},
		{Namespace: "AWS/RDS", MetricName: "WriteIOPS", Value: 5, Unit: "Count/Second", Dimensions: dims, Timestamp: now},
	})
}

func defaultPortFor(engine string) int {
	switch engine {
	case "postgres", "aurora-postgresql":
		return defaultPortPostgres
	default:
		return defaultPort
	}
}

func endpointFor(id, region, kind string) string {
	return fmt.Sprintf("%s.%s.%s.rds.amazonaws.com", id, kind, region)
}

func instanceARN(region, accountID, id string) string {
	return idgen.AWSARN("rds", region, accountID, "db:"+id)
}

func clusterARN(region, accountID, id string) string {
	return idgen.AWSARN("rds", region, accountID, "cluster:"+id)
}

func snapshotARN(region, accountID, id string) string {
	return idgen.AWSARN("rds", region, accountID, "snapshot:"+id)
}

func clusterSnapshotARN(region, accountID, id string) string {
	return idgen.AWSARN("rds", region, accountID, "cluster-snapshot:"+id)
}

func copyTags(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}

	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}

	return out
}

// CreateInstance creates a new database instance.
//
//nolint:gocritic,gocyclo // cfg matches the driver interface signature; complexity comes from sequential field defaulting.
func (m *Mock) CreateInstance(_ context.Context, cfg rdsdriver.InstanceConfig) (*rdsdriver.Instance, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "DBInstanceIdentifier is required")
	}

	if cfg.Engine == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "Engine is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.instances.Get(cfg.ID); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "DB instance %q already exists", cfg.ID)
	}

	if cfg.ClusterID != "" {
		if _, ok := m.clusters.Get(cfg.ClusterID); !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "DB cluster %q not found", cfg.ClusterID)
		}
	}

	port := cfg.Port
	if port == 0 {
		port = defaultPortFor(cfg.Engine)
	}

	storage := cfg.AllocatedStorage
	if storage == 0 {
		storage = defaultStorage
	}

	storageType := cfg.StorageType
	if storageType == "" {
		storageType = defaultStorageType
	}

	instanceClass := cfg.InstanceClass
	if instanceClass == "" {
		instanceClass = defaultInstanceClass
	}

	inst := rdsdriver.Instance{
		ID:                 cfg.ID,
		ARN:                instanceARN(m.opts.Region, m.opts.AccountID, cfg.ID),
		Engine:             cfg.Engine,
		EngineVersion:      cfg.EngineVersion,
		InstanceClass:      instanceClass,
		AllocatedStorage:   storage,
		StorageType:        storageType,
		MasterUsername:     cfg.MasterUsername,
		DBName:             cfg.DBName,
		Endpoint:           endpointFor(cfg.ID, m.opts.Region, "abcd1234"),
		Port:               port,
		State:              rdsdriver.StateAvailable,
		MultiAZ:            cfg.MultiAZ,
		PubliclyAccessible: cfg.PubliclyAccessible,
		VPCSecurityGroups:  append([]string(nil), cfg.VPCSecurityGroups...),
		SubnetGroupName:    cfg.SubnetGroupName,
		ClusterID:          cfg.ClusterID,
		AvailabilityZone:   cfg.AvailabilityZone,
		CreatedAt:          m.opts.Clock.Now().UTC(),
		Tags:               copyTags(cfg.Tags),
	}

	m.instances.Set(cfg.ID, inst)

	if cfg.ClusterID != "" {
		cluster, _ := m.clusters.Get(cfg.ClusterID)
		cluster.Members = append(cluster.Members, cfg.ID)
		m.clusters.Set(cfg.ClusterID, cluster)
	}

	m.emitInstanceMetrics(cfg.ID, cpuMetricRunning, connectionsRunning)

	out := inst

	return &out, nil
}

// DescribeInstances returns all instances if ids is empty, else only matching ones.
//
//nolint:dupl // structurally similar to DescribeClusters but operates on a different store/type.
func (m *Mock) DescribeInstances(_ context.Context, ids []string) ([]rdsdriver.Instance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(ids) == 0 {
		all := m.instances.All()
		out := make([]rdsdriver.Instance, 0, len(all))

		//nolint:gocritic // map values are large structs but we need a flat slice for the API.
		for _, v := range all {
			out = append(out, v)
		}

		return out, nil
	}

	out := make([]rdsdriver.Instance, 0, len(ids))

	for _, id := range ids {
		inst, ok := m.instances.Get(id)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "DB instance %q not found", id)
		}

		out = append(out, inst)
	}

	return out, nil
}

// ModifyInstance applies the supplied changes.
func (m *Mock) ModifyInstance(
	_ context.Context, id string, input rdsdriver.ModifyInstanceInput,
) (*rdsdriver.Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "DB instance %q not found", id)
	}

	if input.InstanceClass != "" {
		inst.InstanceClass = input.InstanceClass
	}

	if input.AllocatedStorage > 0 {
		inst.AllocatedStorage = input.AllocatedStorage
	}

	if input.EngineVersion != "" {
		inst.EngineVersion = input.EngineVersion
	}

	if input.MultiAZ != nil {
		inst.MultiAZ = *input.MultiAZ
	}

	if input.Tags != nil {
		inst.Tags = copyTags(input.Tags)
	}

	m.instances.Set(id, inst)

	out := inst

	return &out, nil
}

// DeleteInstance removes an instance.
func (m *Mock) DeleteInstance(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "DB instance %q not found", id)
	}

	if inst.ClusterID != "" {
		cluster, ok := m.clusters.Get(inst.ClusterID)
		if ok {
			cluster.Members = removeString(cluster.Members, id)
			m.clusters.Set(inst.ClusterID, cluster)
		}
	}

	m.instances.Delete(id)

	return nil
}

// StartInstance moves a stopped instance back to available.
func (m *Mock) StartInstance(_ context.Context, id string) error {
	return m.transitionInstance(id, rdsdriver.StateStopped, rdsdriver.StateAvailable, cpuMetricRunning, connectionsRunning, "start")
}

// StopInstance moves an available instance to stopped.
func (m *Mock) StopInstance(_ context.Context, id string) error {
	return m.transitionInstance(id, rdsdriver.StateAvailable, rdsdriver.StateStopped, cpuMetricStopped, 0, "stop")
}

// RebootInstance cycles an instance through rebooting → available.
func (m *Mock) RebootInstance(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "DB instance %q not found", id)
	}

	if inst.State != rdsdriver.StateAvailable {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"DB instance %q is in state %q; reboot requires %q", id, inst.State, rdsdriver.StateAvailable)
	}

	inst.State = rdsdriver.StateAvailable
	m.instances.Set(id, inst)

	m.emitInstanceMetrics(id, cpuMetricRunning, connectionsRunning)

	return nil
}

func (m *Mock) transitionInstance(id, from, to string, cpu, conns float64, verb string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "DB instance %q not found", id)
	}

	if inst.State == to {
		return nil // idempotent
	}

	if inst.State != from {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"DB instance %q is in state %q; %s requires %q", id, inst.State, verb, from)
	}

	inst.State = to
	m.instances.Set(id, inst)

	m.emitInstanceMetrics(id, cpu, conns)

	return nil
}

// CreateCluster creates an Aurora-style cluster.
//
//nolint:gocritic // cfg matches the driver interface signature.
func (m *Mock) CreateCluster(_ context.Context, cfg rdsdriver.ClusterConfig) (*rdsdriver.Cluster, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "DBClusterIdentifier is required")
	}

	if cfg.Engine == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "Engine is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.clusters.Get(cfg.ID); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "DB cluster %q already exists", cfg.ID)
	}

	port := cfg.Port
	if port == 0 {
		port = defaultPortFor(cfg.Engine)
	}

	cluster := rdsdriver.Cluster{
		ID:                cfg.ID,
		ARN:               clusterARN(m.opts.Region, m.opts.AccountID, cfg.ID),
		Engine:            cfg.Engine,
		EngineVersion:     cfg.EngineVersion,
		MasterUsername:    cfg.MasterUsername,
		DatabaseName:      cfg.DatabaseName,
		Endpoint:          endpointFor(cfg.ID, m.opts.Region, "cluster"),
		ReaderEndpoint:    endpointFor(cfg.ID, m.opts.Region, "cluster-ro"),
		Port:              port,
		State:             rdsdriver.StateAvailable,
		VPCSecurityGroups: append([]string(nil), cfg.VPCSecurityGroups...),
		SubnetGroupName:   cfg.SubnetGroupName,
		CreatedAt:         m.opts.Clock.Now().UTC(),
		Tags:              copyTags(cfg.Tags),
	}

	m.clusters.Set(cfg.ID, cluster)

	out := cluster

	return &out, nil
}

// DescribeClusters returns all clusters if ids is empty, else only matching ones.
//
//nolint:dupl // structurally similar to DescribeInstances but operates on a different store/type.
func (m *Mock) DescribeClusters(_ context.Context, ids []string) ([]rdsdriver.Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(ids) == 0 {
		all := m.clusters.All()
		out := make([]rdsdriver.Cluster, 0, len(all))

		//nolint:gocritic // map values are large structs but we need a flat slice for the API.
		for _, v := range all {
			out = append(out, v)
		}

		return out, nil
	}

	out := make([]rdsdriver.Cluster, 0, len(ids))

	for _, id := range ids {
		cluster, ok := m.clusters.Get(id)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "DB cluster %q not found", id)
		}

		out = append(out, cluster)
	}

	return out, nil
}

// ModifyCluster applies changes.
func (m *Mock) ModifyCluster(
	_ context.Context, id string, input rdsdriver.ModifyInstanceInput,
) (*rdsdriver.Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cluster, ok := m.clusters.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "DB cluster %q not found", id)
	}

	if input.EngineVersion != "" {
		cluster.EngineVersion = input.EngineVersion
	}

	if input.Tags != nil {
		cluster.Tags = copyTags(input.Tags)
	}

	m.clusters.Set(id, cluster)

	out := cluster

	return &out, nil
}

// DeleteCluster removes a cluster (only if it has no members).
func (m *Mock) DeleteCluster(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cluster, ok := m.clusters.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "DB cluster %q not found", id)
	}

	if len(cluster.Members) > 0 {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"DB cluster %q still has %d member instance(s); delete them first", id, len(cluster.Members))
	}

	m.clusters.Delete(id)

	return nil
}

// StartCluster moves a stopped cluster to available.
func (m *Mock) StartCluster(_ context.Context, id string) error {
	return m.transitionCluster(id, rdsdriver.StateStopped, rdsdriver.StateAvailable, "start")
}

// StopCluster moves an available cluster to stopped.
func (m *Mock) StopCluster(_ context.Context, id string) error {
	return m.transitionCluster(id, rdsdriver.StateAvailable, rdsdriver.StateStopped, "stop")
}

func (m *Mock) transitionCluster(id, from, to, verb string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cluster, ok := m.clusters.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "DB cluster %q not found", id)
	}

	if cluster.State == to {
		return nil
	}

	if cluster.State != from {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"DB cluster %q is in state %q; %s requires %q", id, cluster.State, verb, from)
	}

	cluster.State = to
	m.clusters.Set(id, cluster)

	return nil
}

// CreateSnapshot snapshots an instance.
func (m *Mock) CreateSnapshot(_ context.Context, cfg rdsdriver.SnapshotConfig) (*rdsdriver.Snapshot, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "DBSnapshotIdentifier is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances.Get(cfg.InstanceID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "DB instance %q not found", cfg.InstanceID)
	}

	if _, ok := m.snapshots.Get(cfg.ID); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "DB snapshot %q already exists", cfg.ID)
	}

	snap := rdsdriver.Snapshot{
		ID:               cfg.ID,
		ARN:              snapshotARN(m.opts.Region, m.opts.AccountID, cfg.ID),
		InstanceID:       cfg.InstanceID,
		Engine:           inst.Engine,
		EngineVersion:    inst.EngineVersion,
		AllocatedStorage: inst.AllocatedStorage,
		State:            rdsdriver.SnapshotAvailable,
		CreatedAt:        m.opts.Clock.Now().UTC(),
		Tags:             copyTags(cfg.Tags),
	}

	m.snapshots.Set(cfg.ID, snap)

	out := snap

	return &out, nil
}

// DescribeSnapshots returns snapshots filtered by ids and/or instance.
//
//nolint:dupl // structurally similar to DescribeClusterSnapshots but operates on a different store/type.
func (m *Mock) DescribeSnapshots(
	_ context.Context, ids []string, instanceID string,
) ([]rdsdriver.Snapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all := m.snapshots.All()
	idSet := stringSet(ids)

	out := make([]rdsdriver.Snapshot, 0, len(all))

	//nolint:gocritic // map values are sized for accuracy; copy is unavoidable when materializing the result slice.
	for _, snap := range all {
		if instanceID != "" && snap.InstanceID != instanceID {
			continue
		}

		if len(idSet) > 0 {
			if _, ok := idSet[snap.ID]; !ok {
				continue
			}
		}

		out = append(out, snap)
	}

	return out, nil
}

// DeleteSnapshot removes an instance snapshot.
func (m *Mock) DeleteSnapshot(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.snapshots.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "DB snapshot %q not found", id)
	}

	return nil
}

// RestoreInstanceFromSnapshot creates a new instance from a snapshot.
func (m *Mock) RestoreInstanceFromSnapshot(
	_ context.Context, input rdsdriver.RestoreInstanceInput,
) (*rdsdriver.Instance, error) {
	if input.NewInstanceID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "DBInstanceIdentifier is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	snap, ok := m.snapshots.Get(input.SnapshotID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "DB snapshot %q not found", input.SnapshotID)
	}

	if _, ok := m.instances.Get(input.NewInstanceID); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "DB instance %q already exists", input.NewInstanceID)
	}

	instanceClass := input.InstanceClass
	if instanceClass == "" {
		instanceClass = defaultInstanceClass
	}

	now := m.opts.Clock.Now().UTC()

	inst := rdsdriver.Instance{
		ID:               input.NewInstanceID,
		ARN:              instanceARN(m.opts.Region, m.opts.AccountID, input.NewInstanceID),
		Engine:           snap.Engine,
		EngineVersion:    snap.EngineVersion,
		InstanceClass:    instanceClass,
		AllocatedStorage: snap.AllocatedStorage,
		StorageType:      defaultStorageType,
		Endpoint:         endpointFor(input.NewInstanceID, m.opts.Region, "abcd1234"),
		Port:             defaultPortFor(snap.Engine),
		State:            rdsdriver.StateAvailable,
		CreatedAt:        now,
		Tags:             copyTags(input.Tags),
	}

	m.instances.Set(input.NewInstanceID, inst)

	m.emitInstanceMetrics(input.NewInstanceID, cpuMetricRunning, connectionsRunning)

	out := inst

	return &out, nil
}

// CreateClusterSnapshot snapshots a cluster.
func (m *Mock) CreateClusterSnapshot(
	_ context.Context, cfg rdsdriver.ClusterSnapshotConfig,
) (*rdsdriver.ClusterSnapshot, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "DBClusterSnapshotIdentifier is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	cluster, ok := m.clusters.Get(cfg.ClusterID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "DB cluster %q not found", cfg.ClusterID)
	}

	if _, ok := m.clusterSnapshots.Get(cfg.ID); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "DB cluster snapshot %q already exists", cfg.ID)
	}

	snap := rdsdriver.ClusterSnapshot{
		ID:            cfg.ID,
		ARN:           clusterSnapshotARN(m.opts.Region, m.opts.AccountID, cfg.ID),
		ClusterID:     cfg.ClusterID,
		Engine:        cluster.Engine,
		EngineVersion: cluster.EngineVersion,
		State:         rdsdriver.SnapshotAvailable,
		CreatedAt:     m.opts.Clock.Now().UTC(),
		Tags:          copyTags(cfg.Tags),
	}

	m.clusterSnapshots.Set(cfg.ID, snap)

	out := snap

	return &out, nil
}

// DescribeClusterSnapshots returns cluster snapshots filtered by ids and/or cluster.
//
//nolint:dupl // structurally similar to DescribeSnapshots but operates on a different store/type.
func (m *Mock) DescribeClusterSnapshots(
	_ context.Context, ids []string, clusterID string,
) ([]rdsdriver.ClusterSnapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all := m.clusterSnapshots.All()
	idSet := stringSet(ids)

	out := make([]rdsdriver.ClusterSnapshot, 0, len(all))

	//nolint:gocritic // map values are sized for accuracy; copy is unavoidable when materializing the result slice.
	for _, snap := range all {
		if clusterID != "" && snap.ClusterID != clusterID {
			continue
		}

		if len(idSet) > 0 {
			if _, ok := idSet[snap.ID]; !ok {
				continue
			}
		}

		out = append(out, snap)
	}

	return out, nil
}

// DeleteClusterSnapshot removes a cluster snapshot.
func (m *Mock) DeleteClusterSnapshot(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.clusterSnapshots.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "DB cluster snapshot %q not found", id)
	}

	return nil
}

// RestoreClusterFromSnapshot creates a new cluster from a cluster snapshot.
func (m *Mock) RestoreClusterFromSnapshot(
	_ context.Context, input rdsdriver.RestoreClusterInput,
) (*rdsdriver.Cluster, error) {
	if input.NewClusterID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "DBClusterIdentifier is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	snap, ok := m.clusterSnapshots.Get(input.SnapshotID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "DB cluster snapshot %q not found", input.SnapshotID)
	}

	if _, ok := m.clusters.Get(input.NewClusterID); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "DB cluster %q already exists", input.NewClusterID)
	}

	now := m.opts.Clock.Now().UTC()

	cluster := rdsdriver.Cluster{
		ID:             input.NewClusterID,
		ARN:            clusterARN(m.opts.Region, m.opts.AccountID, input.NewClusterID),
		Engine:         snap.Engine,
		EngineVersion:  snap.EngineVersion,
		Endpoint:       endpointFor(input.NewClusterID, m.opts.Region, "cluster"),
		ReaderEndpoint: endpointFor(input.NewClusterID, m.opts.Region, "cluster-ro"),
		Port:           defaultPortFor(snap.Engine),
		State:          rdsdriver.StateAvailable,
		CreatedAt:      now,
		Tags:           copyTags(input.Tags),
	}

	m.clusters.Set(input.NewClusterID, cluster)

	out := cluster

	return &out, nil
}

func removeString(slice []string, target string) []string {
	for i, v := range slice {
		if v == target {
			return append(slice[:i], slice[i+1:]...)
		}
	}

	return slice
}

func stringSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}

	out := make(map[string]struct{}, len(values))
	for _, v := range values {
		out[v] = struct{}{}
	}

	return out
}
