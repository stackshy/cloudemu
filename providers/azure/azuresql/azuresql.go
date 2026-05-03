// Package azuresql provides an in-memory mock of Microsoft.Sql (Azure SQL
// Database). It implements relationaldb/driver.RelationalDB so the same
// backend serves both the portable API (relationaldb.DB) and the SDK-compat
// HTTP layer.
//
// Mapping into the relationaldb shape:
//
//   - Cluster                 → Microsoft.Sql logical server (a connection
//     endpoint that groups databases, billed per-DB).
//   - Instance with ClusterID → A database under that server. The instance ID
//     in the portable API is the DB name; the
//     ClusterID is the server name.
//   - Snapshot                → A database long-term retention backup.
//   - ClusterSnapshot         → Not supported (Azure SQL has no server-level
//     snapshots). Cluster-snapshot methods return
//     InvalidArgument.
//
// Lifecycle: Azure SQL databases are "always on" — there is no native
// start/stop API. The mock still tracks state transitions so portable-API
// users can drive Start/Stop and observe deterministic behavior; the
// transitions don't affect the ARM-visible state.
package azuresql

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
	defaultPort        = 1433
	defaultStorageGB   = 32
	defaultStorageType = "GeneralPurpose"
	defaultSKU         = "GP_S_Gen5_2"
	armProvider        = "Microsoft.Sql"
	cpuMetricRunning   = 0.25
	cpuMetricStopped   = 0.0
	dtuRunning         = 50.0
)

var _ rdsdriver.RelationalDB = (*Mock)(nil)

// Mock is the in-memory Azure SQL implementation.
type Mock struct {
	mu sync.RWMutex

	// clusters key = server name
	clusters *memstore.Store[rdsdriver.Cluster]
	// instances key = "server/database"
	instances *memstore.Store[rdsdriver.Instance]
	// snapshots key = snapshot id
	snapshots *memstore.Store[rdsdriver.Snapshot]

	opts       *config.Options
	monitoring mondriver.Monitoring
}

// New creates a new Azure SQL mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		clusters:  memstore.New[rdsdriver.Cluster](),
		instances: memstore.New[rdsdriver.Instance](),
		snapshots: memstore.New[rdsdriver.Snapshot](),
		opts:      opts,
	}
}

// SetMonitoring wires an Azure-Monitor-style backend for auto-metric emission.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// emitDatabaseMetrics emits Microsoft.Sql/servers/databases-shaped metrics.
func (m *Mock) emitDatabaseMetrics(server, database string, cpuFrac, dtu float64) {
	if m.monitoring == nil {
		return
	}

	now := m.opts.Clock.Now()
	resourceID := serverDatabaseResourceID(m.opts.Region, server, database)
	dims := map[string]string{"resourceId": resourceID}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{
		{Namespace: "Microsoft.Sql/servers/databases", MetricName: "cpu_percent",
			Value: cpuFrac * 100, Unit: "Percent", Dimensions: dims, Timestamp: now},
		{Namespace: "Microsoft.Sql/servers/databases", MetricName: "dtu_consumption_percent",
			Value: dtu, Unit: "Percent", Dimensions: dims, Timestamp: now},
		{Namespace: "Microsoft.Sql/servers/databases", MetricName: "storage_percent",
			Value: 25, Unit: "Percent", Dimensions: dims, Timestamp: now},
		{Namespace: "Microsoft.Sql/servers/databases", MetricName: "connection_successful",
			Value: 5, Unit: "Count", Dimensions: dims, Timestamp: now},
		{Namespace: "Microsoft.Sql/servers/databases", MetricName: "connection_failed",
			Value: 0, Unit: "Count", Dimensions: dims, Timestamp: now},
	})
}

// serverDatabaseResourceID constructs the canonical Azure resource ID for a
// (server, database) pair.
func serverDatabaseResourceID(region, server, database string) string {
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/%s/servers/%s/databases/%s",
		region, region, armProvider, server, database)
}

// instanceKey is the storage key used for an instance in the relationaldb
// model: "{server}/{database}". Encoding the server in the key is what lets
// the same database name exist under different servers without collision.
func instanceKey(server, database string) string {
	return server + "/" + database
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

// CreateInstance creates a new database under an existing logical server.
//
//nolint:gocritic // cfg matches the driver interface signature.
func (m *Mock) CreateInstance(_ context.Context, cfg rdsdriver.InstanceConfig) (*rdsdriver.Instance, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "database name is required")
	}

	if cfg.ClusterID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument,
			"ClusterID (Azure SQL logical server) is required to create a database")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	server, ok := m.clusters.Get(cfg.ClusterID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Azure SQL server %q not found", cfg.ClusterID)
	}

	key := instanceKey(cfg.ClusterID, cfg.ID)
	if _, ok := m.instances.Get(key); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists,
			"database %q already exists on server %q", cfg.ID, cfg.ClusterID)
	}

	storage := cfg.AllocatedStorage
	if storage == 0 {
		storage = defaultStorageGB
	}

	storageType := cfg.StorageType
	if storageType == "" {
		storageType = defaultStorageType
	}

	sku := cfg.InstanceClass
	if sku == "" {
		sku = defaultSKU
	}

	inst := rdsdriver.Instance{
		ID:               cfg.ID,
		ARN:              serverDatabaseResourceID(m.opts.Region, cfg.ClusterID, cfg.ID),
		Engine:           "SQLServer",
		EngineVersion:    cfg.EngineVersion,
		InstanceClass:    sku,
		AllocatedStorage: storage,
		StorageType:      storageType,
		Endpoint:         cfg.ClusterID + ".database.windows.net",
		Port:             defaultPort,
		State:            rdsdriver.StateAvailable,
		ClusterID:        cfg.ClusterID,
		AvailabilityZone: server.SubnetGroupName, // re-use as region carrier
		CreatedAt:        m.opts.Clock.Now().UTC(),
		Tags:             copyTags(cfg.Tags),
	}

	m.instances.Set(key, inst)

	server.Members = append(server.Members, cfg.ID)
	m.clusters.Set(cfg.ClusterID, server)

	m.emitDatabaseMetrics(cfg.ClusterID, cfg.ID, cpuMetricRunning, dtuRunning)

	out := inst

	return &out, nil
}

// DescribeInstances returns instances. With ids empty: all databases across
// all servers. With ids set: each id may be either bare "{database}" (resolved
// against the unique server, ambiguity error otherwise) or the composite
// "{server}/{database}".
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
		inst, err := m.lookupInstance(id)
		if err != nil {
			return nil, err
		}

		out = append(out, inst)
	}

	return out, nil
}

// lookupInstance resolves an id (either "{server}/{db}" or just "{db}") to an
// Instance. Bare names are accepted only when a single matching database
// exists across all servers. Caller must hold m.mu (read or write).
func (m *Mock) lookupInstance(id string) (rdsdriver.Instance, error) {
	if inst, ok := m.instances.Get(id); ok {
		return inst, nil
	}

	matches := m.instances.Filter(func(_ string, inst rdsdriver.Instance) bool {
		return inst.ID == id
	})

	switch len(matches) {
	case 0:
		return rdsdriver.Instance{}, cerrors.Newf(cerrors.NotFound, "database %q not found", id)
	case 1:
		//nolint:gocritic // single iteration to extract the only match; copy unavoidable.
		for _, v := range matches {
			return v, nil
		}
	}

	return rdsdriver.Instance{}, cerrors.Newf(cerrors.InvalidArgument,
		"database name %q is ambiguous across servers; pass {server}/{database}", id)
}

// ModifyInstance applies the supplied changes to a database.
func (m *Mock) ModifyInstance(
	_ context.Context, id string, input rdsdriver.ModifyInstanceInput,
) (*rdsdriver.Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, err := m.lookupInstance(id)
	if err != nil {
		return nil, err
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

	if input.Tags != nil {
		inst.Tags = copyTags(input.Tags)
	}

	m.instances.Set(instanceKey(inst.ClusterID, inst.ID), inst)

	out := inst

	return &out, nil
}

// DeleteInstance removes a database.
func (m *Mock) DeleteInstance(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, err := m.lookupInstance(id)
	if err != nil {
		return err
	}

	m.instances.Delete(instanceKey(inst.ClusterID, inst.ID))

	cluster, ok := m.clusters.Get(inst.ClusterID)
	if ok {
		cluster.Members = removeString(cluster.Members, inst.ID)
		m.clusters.Set(inst.ClusterID, cluster)
	}

	return nil
}

// StartInstance flips a database to available state. Azure SQL doesn't have a
// native start endpoint, so this is observable only via the portable API.
func (m *Mock) StartInstance(_ context.Context, id string) error {
	return m.transitionInstance(id, rdsdriver.StateStopped, rdsdriver.StateAvailable, cpuMetricRunning, dtuRunning, "start")
}

// StopInstance flips a database to stopped state.
func (m *Mock) StopInstance(_ context.Context, id string) error {
	return m.transitionInstance(id, rdsdriver.StateAvailable, rdsdriver.StateStopped, cpuMetricStopped, 0, "stop")
}

// RebootInstance is a no-op state-wise; emits a fresh metric tick.
func (m *Mock) RebootInstance(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, err := m.lookupInstance(id)
	if err != nil {
		return err
	}

	if inst.State != rdsdriver.StateAvailable {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"database %q is in state %q; reboot requires %q", id, inst.State, rdsdriver.StateAvailable)
	}

	m.emitDatabaseMetrics(inst.ClusterID, inst.ID, cpuMetricRunning, dtuRunning)

	return nil
}

func (m *Mock) transitionInstance(id, from, to string, cpu, dtu float64, verb string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, err := m.lookupInstance(id)
	if err != nil {
		return err
	}

	if inst.State == to {
		return nil // idempotent
	}

	if inst.State != from {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"database %q is in state %q; %s requires %q", id, inst.State, verb, from)
	}

	inst.State = to
	m.instances.Set(instanceKey(inst.ClusterID, inst.ID), inst)

	m.emitDatabaseMetrics(inst.ClusterID, inst.ID, cpu, dtu)

	return nil
}

// CreateCluster creates an Azure SQL logical server.
//
//nolint:gocritic // cfg matches the driver interface signature.
func (m *Mock) CreateCluster(_ context.Context, cfg rdsdriver.ClusterConfig) (*rdsdriver.Cluster, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "server name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.clusters.Get(cfg.ID); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "Azure SQL server %q already exists", cfg.ID)
	}

	cluster := rdsdriver.Cluster{
		ID:             cfg.ID,
		ARN:            idgen.AzureID(m.opts.Region, m.opts.Region, armProvider, "servers", cfg.ID),
		Engine:         "SQLServer",
		EngineVersion:  cfg.EngineVersion,
		MasterUsername: cfg.MasterUsername,
		Endpoint:       cfg.ID + ".database.windows.net",
		Port:           defaultPort,
		State:          rdsdriver.StateAvailable,
		// Stash region in SubnetGroupName since the field exists; consumers
		// can read it back from there.
		SubnetGroupName: m.opts.Region,
		CreatedAt:       m.opts.Clock.Now().UTC(),
		Tags:            copyTags(cfg.Tags),
	}

	m.clusters.Set(cfg.ID, cluster)

	out := cluster

	return &out, nil
}

// DescribeClusters returns servers (or all if ids empty).
func (m *Mock) DescribeClusters(_ context.Context, ids []string) ([]rdsdriver.Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(ids) == 0 {
		all := m.clusters.All()
		out := make([]rdsdriver.Cluster, 0, len(all))

		//nolint:gocritic // map values are large structs; copy is unavoidable when materializing the result slice.
		for _, v := range all {
			out = append(out, v)
		}

		return out, nil
	}

	out := make([]rdsdriver.Cluster, 0, len(ids))

	for _, id := range ids {
		cluster, ok := m.clusters.Get(id)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "Azure SQL server %q not found", id)
		}

		out = append(out, cluster)
	}

	return out, nil
}

// ModifyCluster updates server-level fields (admin password reset, version).
func (m *Mock) ModifyCluster(
	_ context.Context, id string, input rdsdriver.ModifyInstanceInput,
) (*rdsdriver.Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cluster, ok := m.clusters.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Azure SQL server %q not found", id)
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

// DeleteCluster removes a server. Real Azure cascade-deletes child databases;
// the mock matches that behavior so tests don't have to manually clean up.
func (m *Mock) DeleteCluster(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cluster, ok := m.clusters.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "Azure SQL server %q not found", id)
	}

	for _, member := range cluster.Members {
		m.instances.Delete(instanceKey(id, member))
	}

	m.clusters.Delete(id)

	return nil
}

// StartCluster / StopCluster are no-ops on Azure SQL servers. They aren't
// "started" / "stopped" the way RDS clusters are; the underlying databases
// are independently controlled.
func (*Mock) StartCluster(_ context.Context, _ string) error { return nil }

// StopCluster is a no-op on Azure SQL servers.
func (*Mock) StopCluster(_ context.Context, _ string) error { return nil }

// CreateSnapshot creates a long-term retention backup of a database.
func (m *Mock) CreateSnapshot(_ context.Context, cfg rdsdriver.SnapshotConfig) (*rdsdriver.Snapshot, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "snapshot id is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	inst, err := m.lookupInstance(cfg.InstanceID)
	if err != nil {
		return nil, err
	}

	if _, ok := m.snapshots.Get(cfg.ID); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "backup %q already exists", cfg.ID)
	}

	snap := rdsdriver.Snapshot{
		ID:               cfg.ID,
		ARN:              idgen.AzureID(m.opts.Region, m.opts.Region, armProvider, "longTermRetentionBackups", cfg.ID),
		InstanceID:       instanceKey(inst.ClusterID, inst.ID),
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

// DeleteSnapshot removes a backup.
func (m *Mock) DeleteSnapshot(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.snapshots.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "backup %q not found", id)
	}

	return nil
}

// RestoreInstanceFromSnapshot creates a new database from a backup.
func (m *Mock) RestoreInstanceFromSnapshot(
	_ context.Context, input rdsdriver.RestoreInstanceInput,
) (*rdsdriver.Instance, error) {
	if input.NewInstanceID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "new database id is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	snap, ok := m.snapshots.Get(input.SnapshotID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "backup %q not found", input.SnapshotID)
	}

	// NewInstanceID can be either bare ("db1") or composite ("server/db1").
	// In bare form the snapshot's source server is reused.
	server, dbName := splitInstanceKey(input.NewInstanceID)

	if server == "" {
		// Pull source server from snapshot key.
		srv, _ := splitInstanceKey(snap.InstanceID)
		server = srv
	}

	if _, exists := m.clusters.Get(server); !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "Azure SQL server %q not found", server)
	}

	key := instanceKey(server, dbName)
	if _, exists := m.instances.Get(key); exists {
		return nil, cerrors.Newf(cerrors.AlreadyExists,
			"database %q already exists on server %q", dbName, server)
	}

	sku := input.InstanceClass
	if sku == "" {
		sku = defaultSKU
	}

	now := m.opts.Clock.Now().UTC()

	inst := rdsdriver.Instance{
		ID:               dbName,
		ARN:              serverDatabaseResourceID(m.opts.Region, server, dbName),
		Engine:           snap.Engine,
		EngineVersion:    snap.EngineVersion,
		InstanceClass:    sku,
		AllocatedStorage: snap.AllocatedStorage,
		StorageType:      defaultStorageType,
		Endpoint:         server + ".database.windows.net",
		Port:             defaultPort,
		State:            rdsdriver.StateAvailable,
		ClusterID:        server,
		CreatedAt:        now,
		Tags:             copyTags(input.Tags),
	}

	m.instances.Set(key, inst)

	cluster, ok := m.clusters.Get(server)
	if ok {
		cluster.Members = append(cluster.Members, dbName)
		m.clusters.Set(server, cluster)
	}

	m.emitDatabaseMetrics(server, dbName, cpuMetricRunning, dtuRunning)

	out := inst

	return &out, nil
}

// CreateClusterSnapshot is unsupported on Azure SQL.
func (*Mock) CreateClusterSnapshot(
	_ context.Context, _ rdsdriver.ClusterSnapshotConfig,
) (*rdsdriver.ClusterSnapshot, error) {
	return nil, cerrors.New(cerrors.InvalidArgument,
		"Azure SQL does not support server-level snapshots; backups are per-database")
}

// DescribeClusterSnapshots returns an empty list — Azure SQL has none.
func (*Mock) DescribeClusterSnapshots(
	_ context.Context, _ []string, _ string,
) ([]rdsdriver.ClusterSnapshot, error) {
	return []rdsdriver.ClusterSnapshot{}, nil
}

// DeleteClusterSnapshot is unsupported on Azure SQL.
func (*Mock) DeleteClusterSnapshot(_ context.Context, _ string) error {
	return cerrors.New(cerrors.InvalidArgument, "Azure SQL does not support server-level snapshots")
}

// RestoreClusterFromSnapshot is unsupported on Azure SQL.
func (*Mock) RestoreClusterFromSnapshot(
	_ context.Context, _ rdsdriver.RestoreClusterInput,
) (*rdsdriver.Cluster, error) {
	return nil, cerrors.New(cerrors.InvalidArgument,
		"Azure SQL does not support server-level snapshot restore")
}

func splitInstanceKey(id string) (server, database string) {
	for i, ch := range id {
		if ch == '/' {
			return id[:i], id[i+1:]
		}
	}

	return "", id
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
