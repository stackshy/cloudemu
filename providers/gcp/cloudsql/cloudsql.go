// Package cloudsql provides an in-memory mock of GCP Cloud SQL. It
// implements relationaldb/driver.RelationalDB so the same backend serves both
// the portable API (relationaldb.DB) and the SDK-compat HTTP layer.
//
// Cloud SQL has no Aurora-style cluster concept — the cluster methods on the
// driver interface return InvalidArgument. Instances and snapshots (a.k.a.
// "backup runs" in Cloud SQL terminology) are fully supported, as is
// restore-from-backup.
package cloudsql

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
	defaultPortMySQL     = 3306
	defaultPortPostgres  = 5432
	defaultPortSQLServer = 1433
	defaultStorage       = 10
	defaultStorageType   = "PD_SSD"
	defaultTier          = "db-f1-micro"
	cpuMetricRunning     = 0.25 // GCP reports CPU as 0.0–1.0 fraction.
	cpuMetricStopped     = 0.0
	connRunning          = 5.0
)

var _ rdsdriver.RelationalDB = (*Mock)(nil)

// Mock is the in-memory GCP Cloud SQL implementation.
type Mock struct {
	mu sync.RWMutex

	instances *memstore.Store[rdsdriver.Instance]
	snapshots *memstore.Store[rdsdriver.Snapshot]

	opts       *config.Options
	monitoring mondriver.Monitoring
}

// New creates a new Cloud SQL mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		instances: memstore.New[rdsdriver.Instance](),
		snapshots: memstore.New[rdsdriver.Snapshot](),
		opts:      opts,
	}
}

// SetMonitoring wires a Cloud Monitoring backend for auto-metric emission.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// Cloud SQL emits utilization-style metrics on the
// cloudsql.googleapis.com namespace. The metric type strings come straight
// from the GCP metric explorer.
func (m *Mock) emitInstanceMetrics(instanceID string, cpuFrac, connections float64) {
	if m.monitoring == nil {
		return
	}

	now := m.opts.Clock.Now()
	dims := map[string]string{"database_id": m.opts.ProjectID + ":" + instanceID}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{
		{Namespace: "cloudsql.googleapis.com", MetricName: "database/cpu/utilization",
			Value: cpuFrac, Unit: "1", Dimensions: dims, Timestamp: now},
		{Namespace: "cloudsql.googleapis.com", MetricName: "database/network/connections",
			Value: connections, Unit: "1", Dimensions: dims, Timestamp: now},
		{Namespace: "cloudsql.googleapis.com", MetricName: "database/memory/utilization",
			Value: 0.4, Unit: "1", Dimensions: dims, Timestamp: now},
		{Namespace: "cloudsql.googleapis.com", MetricName: "database/disk/read_ops_count",
			Value: 10, Unit: "1", Dimensions: dims, Timestamp: now},
		{Namespace: "cloudsql.googleapis.com", MetricName: "database/disk/write_ops_count",
			Value: 5, Unit: "1", Dimensions: dims, Timestamp: now},
	})
}

func defaultPortFor(engine string) int {
	switch engine {
	case "POSTGRES_15", "POSTGRES_14", "POSTGRES_13", "postgres", "postgresql":
		return defaultPortPostgres
	case "SQLSERVER_2019_STANDARD", "SQLSERVER_2017_STANDARD", "sqlserver":
		return defaultPortSQLServer
	default:
		return defaultPortMySQL
	}
}

func instanceConnectionName(project, region, id string) string {
	return fmt.Sprintf("%s:%s:%s", project, region, id)
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

// CreateInstance creates a new Cloud SQL instance.
//
//nolint:gocritic // cfg matches the driver interface signature.
func (m *Mock) CreateInstance(_ context.Context, cfg rdsdriver.InstanceConfig) (*rdsdriver.Instance, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "instance name is required")
	}

	if cfg.Engine == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "databaseVersion is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.instances.Get(cfg.ID); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "Cloud SQL instance %q already exists", cfg.ID)
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

	tier := cfg.InstanceClass
	if tier == "" {
		tier = defaultTier
	}

	region := cfg.AvailabilityZone
	if region == "" {
		region = m.opts.Region
	}

	inst := rdsdriver.Instance{
		ID:                 cfg.ID,
		ARN:                idgen.GCPID(m.opts.ProjectID, "instances", cfg.ID),
		Engine:             cfg.Engine,
		EngineVersion:      cfg.EngineVersion,
		InstanceClass:      tier,
		AllocatedStorage:   storage,
		StorageType:        storageType,
		MasterUsername:     cfg.MasterUsername,
		DBName:             cfg.DBName,
		Endpoint:           instanceConnectionName(m.opts.ProjectID, region, cfg.ID),
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

	m.emitInstanceMetrics(cfg.ID, cpuMetricRunning, connRunning)

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
			return nil, cerrors.Newf(cerrors.NotFound, "Cloud SQL instance %q not found", id)
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
		return nil, cerrors.Newf(cerrors.NotFound, "Cloud SQL instance %q not found", id)
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

	if !m.instances.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "Cloud SQL instance %q not found", id)
	}

	return nil
}

// StartInstance moves a stopped instance back to runnable. In Cloud SQL this
// corresponds to setting settings.activationPolicy=ALWAYS.
func (m *Mock) StartInstance(_ context.Context, id string) error {
	return m.transitionInstance(id, rdsdriver.StateStopped, rdsdriver.StateAvailable, cpuMetricRunning, connRunning, "start")
}

// StopInstance moves a runnable instance to stopped. In Cloud SQL this
// corresponds to setting settings.activationPolicy=NEVER.
func (m *Mock) StopInstance(_ context.Context, id string) error {
	return m.transitionInstance(id, rdsdriver.StateAvailable, rdsdriver.StateStopped, cpuMetricStopped, 0, "stop")
}

// RebootInstance cycles an instance through rebooting. In Cloud SQL this
// corresponds to the :restart endpoint.
func (m *Mock) RebootInstance(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "Cloud SQL instance %q not found", id)
	}

	if inst.State != rdsdriver.StateAvailable {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"Cloud SQL instance %q is in state %q; restart requires %q", id, inst.State, rdsdriver.StateAvailable)
	}

	m.instances.Set(id, inst)

	m.emitInstanceMetrics(id, cpuMetricRunning, connRunning)

	return nil
}

func (m *Mock) transitionInstance(id, from, to string, cpu, conns float64, verb string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "Cloud SQL instance %q not found", id)
	}

	if inst.State == to {
		return nil // idempotent
	}

	if inst.State != from {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"Cloud SQL instance %q is in state %q; %s requires %q", id, inst.State, verb, from)
	}

	inst.State = to
	m.instances.Set(id, inst)

	m.emitInstanceMetrics(id, cpu, conns)

	return nil
}

// CreateCluster is unsupported on Cloud SQL — it has no Aurora-style cluster.
//
//nolint:gocritic // signature matches the driver interface.
func (*Mock) CreateCluster(_ context.Context, _ rdsdriver.ClusterConfig) (*rdsdriver.Cluster, error) {
	return nil, cerrors.New(cerrors.InvalidArgument,
		"Cloud SQL does not support Aurora-style clusters; use replicas instead")
}

// DescribeClusters returns an empty list — Cloud SQL has no clusters.
func (*Mock) DescribeClusters(_ context.Context, _ []string) ([]rdsdriver.Cluster, error) {
	return []rdsdriver.Cluster{}, nil
}

// ModifyCluster is unsupported on Cloud SQL.
func (*Mock) ModifyCluster(
	_ context.Context, _ string, _ rdsdriver.ModifyInstanceInput,
) (*rdsdriver.Cluster, error) {
	return nil, cerrors.New(cerrors.InvalidArgument, "Cloud SQL does not support clusters")
}

// DeleteCluster is unsupported on Cloud SQL.
func (*Mock) DeleteCluster(_ context.Context, _ string) error {
	return cerrors.New(cerrors.InvalidArgument, "Cloud SQL does not support clusters")
}

// StartCluster is unsupported on Cloud SQL.
func (*Mock) StartCluster(_ context.Context, _ string) error {
	return cerrors.New(cerrors.InvalidArgument, "Cloud SQL does not support clusters")
}

// StopCluster is unsupported on Cloud SQL.
func (*Mock) StopCluster(_ context.Context, _ string) error {
	return cerrors.New(cerrors.InvalidArgument, "Cloud SQL does not support clusters")
}

// CreateSnapshot creates a backup run for an instance. Cloud SQL calls
// these "backup runs"; the portable API exposes them as snapshots.
func (m *Mock) CreateSnapshot(_ context.Context, cfg rdsdriver.SnapshotConfig) (*rdsdriver.Snapshot, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "snapshot id is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances.Get(cfg.InstanceID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Cloud SQL instance %q not found", cfg.InstanceID)
	}

	if _, ok := m.snapshots.Get(cfg.ID); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "backup run %q already exists", cfg.ID)
	}

	snap := rdsdriver.Snapshot{
		ID:               cfg.ID,
		ARN:              idgen.GCPID(m.opts.ProjectID, "instances/"+cfg.InstanceID+"/backupRuns", cfg.ID),
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

// DeleteSnapshot removes a backup run.
func (m *Mock) DeleteSnapshot(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.snapshots.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "backup run %q not found", id)
	}

	return nil
}

// RestoreInstanceFromSnapshot creates a new instance from a backup run.
func (m *Mock) RestoreInstanceFromSnapshot(
	_ context.Context, input rdsdriver.RestoreInstanceInput,
) (*rdsdriver.Instance, error) {
	if input.NewInstanceID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "new instance id is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	snap, ok := m.snapshots.Get(input.SnapshotID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "backup run %q not found", input.SnapshotID)
	}

	if _, ok := m.instances.Get(input.NewInstanceID); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "Cloud SQL instance %q already exists", input.NewInstanceID)
	}

	tier := input.InstanceClass
	if tier == "" {
		tier = defaultTier
	}

	now := m.opts.Clock.Now().UTC()

	inst := rdsdriver.Instance{
		ID:               input.NewInstanceID,
		ARN:              idgen.GCPID(m.opts.ProjectID, "instances", input.NewInstanceID),
		Engine:           snap.Engine,
		EngineVersion:    snap.EngineVersion,
		InstanceClass:    tier,
		AllocatedStorage: snap.AllocatedStorage,
		StorageType:      defaultStorageType,
		Endpoint:         instanceConnectionName(m.opts.ProjectID, m.opts.Region, input.NewInstanceID),
		Port:             defaultPortFor(snap.Engine),
		State:            rdsdriver.StateAvailable,
		AvailabilityZone: m.opts.Region,
		CreatedAt:        now,
		Tags:             copyTags(input.Tags),
	}

	m.instances.Set(input.NewInstanceID, inst)

	m.emitInstanceMetrics(input.NewInstanceID, cpuMetricRunning, connRunning)

	out := inst

	return &out, nil
}

// CreateClusterSnapshot is unsupported on Cloud SQL.
func (*Mock) CreateClusterSnapshot(
	_ context.Context, _ rdsdriver.ClusterSnapshotConfig,
) (*rdsdriver.ClusterSnapshot, error) {
	return nil, cerrors.New(cerrors.InvalidArgument, "Cloud SQL does not support cluster snapshots")
}

// DescribeClusterSnapshots returns an empty list — Cloud SQL has no clusters.
func (*Mock) DescribeClusterSnapshots(
	_ context.Context, _ []string, _ string,
) ([]rdsdriver.ClusterSnapshot, error) {
	return []rdsdriver.ClusterSnapshot{}, nil
}

// DeleteClusterSnapshot is unsupported on Cloud SQL.
func (*Mock) DeleteClusterSnapshot(_ context.Context, _ string) error {
	return cerrors.New(cerrors.InvalidArgument, "Cloud SQL does not support cluster snapshots")
}

// RestoreClusterFromSnapshot is unsupported on Cloud SQL.
func (*Mock) RestoreClusterFromSnapshot(
	_ context.Context, _ rdsdriver.RestoreClusterInput,
) (*rdsdriver.Cluster, error) {
	return nil, cerrors.New(cerrors.InvalidArgument, "Cloud SQL does not support clusters")
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
