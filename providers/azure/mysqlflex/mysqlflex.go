// Package mysqlflex provides an in-memory mock of Azure Database for MySQL —
// Flexible Server. It implements relationaldb/driver.RelationalDB so the same
// backend serves both the portable API (relationaldb.DB) and the SDK-compat
// HTTP layer (server/azure/mysqlflex).
//
// MySQL Flexible Server is flat: a flexibleServer maps directly to a database
// "instance"; there is no Aurora-style cluster. Cluster methods on the driver
// interface return InvalidArgument. Snapshots are exposed as portable-only
// synthetic backups (Azure surfaces backups via the dataprotection RP, not
// flexibleServers itself); ClusterSnapshot ops are unsupported.
package mysqlflex

import (
	"context"
	"sync"

	"github.com/stackshy/cloudemu/config"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	rdsdriver "github.com/stackshy/cloudemu/relationaldb/driver"
)

const (
	defaultPort           = 3306
	defaultStorageGB      = 20
	defaultStorageType    = "Premium_LRS"
	defaultSKU            = "Standard_B1ms"
	defaultEngine         = "MySQL"
	resourceGroupTag      = "cloud-mock"
	providerNamespace     = "Microsoft.DBforMySQL"
	resourceTypeFlexible  = "flexibleServers"
	endpointSuffix        = ".mysql.database.azure.com"
	cpuMetricRunning      = 25.0
	cpuMetricStopped      = 0.0
	connectionMetricValue = 5.0
	storageUsedFraction   = 0.4
	diskReadOpsRunning    = 10.0
	diskWriteOpsRunning   = 5.0
)

var _ rdsdriver.RelationalDB = (*Mock)(nil)

// Mock is the in-memory Azure MySQL Flexible Server implementation.
type Mock struct {
	mu sync.RWMutex

	instances *memstore.Store[rdsdriver.Instance]
	snapshots *memstore.Store[rdsdriver.Snapshot]

	opts       *config.Options
	monitoring mondriver.Monitoring
}

// New creates a new MySQL Flexible Server mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		instances: memstore.New[rdsdriver.Instance](),
		snapshots: memstore.New[rdsdriver.Snapshot](),
		opts:      opts,
	}
}

// SetMonitoring wires an Azure Monitor backend for auto-metric emission.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// emitInstanceMetrics pushes a representative datapoint set on the
// Microsoft.DBforMySQL/flexibleServers namespace, matching the metric names
// that real Azure Monitor surfaces for MySQL Flex.
func (m *Mock) emitInstanceMetrics(instanceID string, cpuPct, connections, readOps, writeOps float64) {
	if m.monitoring == nil {
		return
	}

	now := m.opts.Clock.Now()
	dims := map[string]string{"resourceId": m.armResourceID(instanceID)}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{
		{Namespace: providerNamespace + "/" + resourceTypeFlexible, MetricName: "cpu_percent",
			Value: cpuPct, Unit: "Percent", Dimensions: dims, Timestamp: now},
		{Namespace: providerNamespace + "/" + resourceTypeFlexible, MetricName: "active_connections",
			Value: connections, Unit: "Count", Dimensions: dims, Timestamp: now},
		{Namespace: providerNamespace + "/" + resourceTypeFlexible, MetricName: "storage_used",
			Value: storageUsedFraction, Unit: "Bytes", Dimensions: dims, Timestamp: now},
		{Namespace: providerNamespace + "/" + resourceTypeFlexible, MetricName: "io_consumption_percent",
			Value: readOps, Unit: "Percent", Dimensions: dims, Timestamp: now},
		{Namespace: providerNamespace + "/" + resourceTypeFlexible, MetricName: "iops",
			Value: writeOps, Unit: "Count", Dimensions: dims, Timestamp: now},
	})
}

func (m *Mock) armResourceID(instanceID string) string {
	return idgen.AzureID(m.opts.AccountID, resourceGroupTag, providerNamespace, resourceTypeFlexible, instanceID)
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

// CreateInstance creates a new MySQL Flexible Server.
//
//nolint:gocritic // cfg matches the driver interface signature.
func (m *Mock) CreateInstance(_ context.Context, cfg rdsdriver.InstanceConfig) (*rdsdriver.Instance, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "server name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.instances.Get(cfg.ID); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "MySQL Flexible Server %q already exists", cfg.ID)
	}

	port := cfg.Port
	if port == 0 {
		port = defaultPort
	}

	storage := cfg.AllocatedStorage
	if storage == 0 {
		storage = defaultStorageGB
	}

	storageType := cfg.StorageType
	if storageType == "" {
		storageType = defaultStorageType
	}

	tier := cfg.InstanceClass
	if tier == "" {
		tier = defaultSKU
	}

	engine := cfg.Engine
	if engine == "" {
		engine = defaultEngine
	}

	region := cfg.AvailabilityZone
	if region == "" {
		region = m.opts.Region
	}

	inst := rdsdriver.Instance{
		ID:                 cfg.ID,
		ARN:                m.armResourceID(cfg.ID),
		Engine:             engine,
		EngineVersion:      cfg.EngineVersion,
		InstanceClass:      tier,
		AllocatedStorage:   storage,
		StorageType:        storageType,
		MasterUsername:     cfg.MasterUsername,
		DBName:             cfg.DBName,
		Endpoint:           cfg.ID + endpointSuffix,
		Port:               port,
		State:              rdsdriver.StateAvailable,
		MultiAZ:            cfg.MultiAZ,
		PubliclyAccessible: cfg.PubliclyAccessible,
		VPCSecurityGroups:  append([]string(nil), cfg.VPCSecurityGroups...),
		SubnetGroupName:    cfg.SubnetGroupName,
		AvailabilityZone:   region,
		CreatedAt:          m.opts.Clock.Now().UTC(),
		Tags:               copyTags(cfg.Tags),
	}

	m.instances.Set(cfg.ID, inst)

	m.emitInstanceMetrics(cfg.ID, cpuMetricRunning, connectionMetricValue, diskReadOpsRunning, diskWriteOpsRunning)

	out := inst

	return &out, nil
}

// DescribeInstances returns all instances if ids is empty, else only matching ones.
func (m *Mock) DescribeInstances(_ context.Context, ids []string) ([]rdsdriver.Instance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(ids) == 0 {
		all := m.instances.All()
		out := make([]rdsdriver.Instance, 0, len(all))

		//nolint:gocritic // map values are large structs; copy is unavoidable when materializing the result slice.
		for _, v := range all {
			out = append(out, v)
		}

		return out, nil
	}

	out := make([]rdsdriver.Instance, 0, len(ids))

	for _, id := range ids {
		inst, ok := m.instances.Get(id)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "MySQL Flexible Server %q not found", id)
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
		return nil, cerrors.Newf(cerrors.NotFound, "MySQL Flexible Server %q not found", id)
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

// DeleteInstance removes a server.
func (m *Mock) DeleteInstance(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.instances.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "MySQL Flexible Server %q not found", id)
	}

	return nil
}

// StartInstance moves a stopped server back to runnable.
func (m *Mock) StartInstance(_ context.Context, id string) error {
	return m.transitionInstance(id, rdsdriver.StateStopped, rdsdriver.StateAvailable,
		cpuMetricRunning, connectionMetricValue, diskReadOpsRunning, diskWriteOpsRunning, "start")
}

// StopInstance moves a runnable server to stopped.
func (m *Mock) StopInstance(_ context.Context, id string) error {
	return m.transitionInstance(id, rdsdriver.StateAvailable, rdsdriver.StateStopped,
		cpuMetricStopped, 0, 0, 0, "stop")
}

// RebootInstance restarts a running server.
func (m *Mock) RebootInstance(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "MySQL Flexible Server %q not found", id)
	}

	if inst.State != rdsdriver.StateAvailable {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"MySQL Flexible Server %q is in state %q; restart requires %q", id, inst.State, rdsdriver.StateAvailable)
	}

	m.instances.Set(id, inst)

	m.emitInstanceMetrics(id, cpuMetricRunning, connectionMetricValue, diskReadOpsRunning, diskWriteOpsRunning)

	return nil
}

func (m *Mock) transitionInstance(id, from, to string, cpu, conns, readOps, writeOps float64, verb string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "MySQL Flexible Server %q not found", id)
	}

	if inst.State == to {
		return nil // idempotent
	}

	if inst.State != from {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"MySQL Flexible Server %q is in state %q; %s requires %q", id, inst.State, verb, from)
	}

	inst.State = to
	m.instances.Set(id, inst)

	m.emitInstanceMetrics(id, cpu, conns, readOps, writeOps)

	return nil
}

// CreateCluster is unsupported on MySQL Flexible Server.
//
//nolint:gocritic // signature matches the driver interface.
func (*Mock) CreateCluster(_ context.Context, _ rdsdriver.ClusterConfig) (*rdsdriver.Cluster, error) {
	return nil, cerrors.New(cerrors.InvalidArgument,
		"MySQL Flexible Server does not support Aurora-style clusters; use replicas instead")
}

// DescribeClusters returns an empty list — Flex MySQL has no clusters.
func (*Mock) DescribeClusters(_ context.Context, _ []string) ([]rdsdriver.Cluster, error) {
	return []rdsdriver.Cluster{}, nil
}

// ModifyCluster is unsupported on MySQL Flexible Server.
func (*Mock) ModifyCluster(
	_ context.Context, _ string, _ rdsdriver.ModifyInstanceInput,
) (*rdsdriver.Cluster, error) {
	return nil, cerrors.New(cerrors.InvalidArgument, "MySQL Flexible Server does not support clusters")
}

// DeleteCluster is unsupported on MySQL Flexible Server.
func (*Mock) DeleteCluster(_ context.Context, _ string) error {
	return cerrors.New(cerrors.InvalidArgument, "MySQL Flexible Server does not support clusters")
}

// StartCluster is unsupported on MySQL Flexible Server.
func (*Mock) StartCluster(_ context.Context, _ string) error {
	return cerrors.New(cerrors.InvalidArgument, "MySQL Flexible Server does not support clusters")
}

// StopCluster is unsupported on MySQL Flexible Server.
func (*Mock) StopCluster(_ context.Context, _ string) error {
	return cerrors.New(cerrors.InvalidArgument, "MySQL Flexible Server does not support clusters")
}

// CreateSnapshot creates a portable-only synthetic backup. Azure exposes MySQL
// Flex backups via Microsoft.DataProtection — not flexibleServers itself — so
// the SDK-compat handler does not surface this op. Portable callers can still
// drive backups through relationaldb.DB.
func (m *Mock) CreateSnapshot(_ context.Context, cfg rdsdriver.SnapshotConfig) (*rdsdriver.Snapshot, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "snapshot id is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances.Get(cfg.InstanceID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "MySQL Flexible Server %q not found", cfg.InstanceID)
	}

	if _, ok := m.snapshots.Get(cfg.ID); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "backup %q already exists", cfg.ID)
	}

	snap := rdsdriver.Snapshot{
		ID:               cfg.ID,
		ARN:              idgen.AzureID(m.opts.AccountID, resourceGroupTag, providerNamespace, "backups", cfg.ID),
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

// DeleteSnapshot removes a portable backup.
func (m *Mock) DeleteSnapshot(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.snapshots.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "backup %q not found", id)
	}

	return nil
}

// RestoreInstanceFromSnapshot creates a new server from a portable backup.
func (m *Mock) RestoreInstanceFromSnapshot(
	_ context.Context, input rdsdriver.RestoreInstanceInput,
) (*rdsdriver.Instance, error) {
	if input.NewInstanceID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "new server name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	snap, ok := m.snapshots.Get(input.SnapshotID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "backup %q not found", input.SnapshotID)
	}

	if _, ok := m.instances.Get(input.NewInstanceID); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists,
			"MySQL Flexible Server %q already exists", input.NewInstanceID)
	}

	tier := input.InstanceClass
	if tier == "" {
		tier = defaultSKU
	}

	now := m.opts.Clock.Now().UTC()

	inst := rdsdriver.Instance{
		ID:               input.NewInstanceID,
		ARN:              m.armResourceID(input.NewInstanceID),
		Engine:           snap.Engine,
		EngineVersion:    snap.EngineVersion,
		InstanceClass:    tier,
		AllocatedStorage: snap.AllocatedStorage,
		StorageType:      defaultStorageType,
		Endpoint:         input.NewInstanceID + endpointSuffix,
		Port:             defaultPort,
		State:            rdsdriver.StateAvailable,
		AvailabilityZone: m.opts.Region,
		CreatedAt:        now,
		Tags:             copyTags(input.Tags),
	}

	m.instances.Set(input.NewInstanceID, inst)

	m.emitInstanceMetrics(input.NewInstanceID, cpuMetricRunning, connectionMetricValue, diskReadOpsRunning, diskWriteOpsRunning)

	out := inst

	return &out, nil
}

// CreateClusterSnapshot is unsupported on MySQL Flexible Server.
func (*Mock) CreateClusterSnapshot(
	_ context.Context, _ rdsdriver.ClusterSnapshotConfig,
) (*rdsdriver.ClusterSnapshot, error) {
	return nil, cerrors.New(cerrors.InvalidArgument, "MySQL Flexible Server does not support cluster snapshots")
}

// DescribeClusterSnapshots returns an empty list — Flex MySQL has no clusters.
func (*Mock) DescribeClusterSnapshots(
	_ context.Context, _ []string, _ string,
) ([]rdsdriver.ClusterSnapshot, error) {
	return []rdsdriver.ClusterSnapshot{}, nil
}

// DeleteClusterSnapshot is unsupported on MySQL Flexible Server.
func (*Mock) DeleteClusterSnapshot(_ context.Context, _ string) error {
	return cerrors.New(cerrors.InvalidArgument, "MySQL Flexible Server does not support cluster snapshots")
}

// RestoreClusterFromSnapshot is unsupported on MySQL Flexible Server.
func (*Mock) RestoreClusterFromSnapshot(
	_ context.Context, _ rdsdriver.RestoreClusterInput,
) (*rdsdriver.Cluster, error) {
	return nil, cerrors.New(cerrors.InvalidArgument, "MySQL Flexible Server does not support clusters")
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
