// Package postgresflex provides an in-memory mock of Microsoft.DBforPostgreSQL
// (Azure Database for PostgreSQL — Flexible Server). It implements
// relationaldb/driver.RelationalDB so the same backend serves both the
// portable API (relationaldb.DB) and the SDK-compat HTTP layer.
//
// Postgres Flex has a flat resource model: each flexible server is a
// standalone managed Postgres instance — there is no Aurora-style cluster
// concept. The driver's Cluster*/ClusterSnapshot* methods therefore return
// InvalidArgument. Snapshots are exposed via the portable API only; there is
// no ARM endpoint for them in the MVP.
//
// Lifecycle: Postgres Flex supports start/stop/restart natively. The mock
// tracks state transitions and emits Microsoft.DBforPostgreSQL/flexibleServers
// metrics on each transition.
package postgresflex

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	dbengine "github.com/stackshy/cloudemu/v2/services/relationaldb/dbengine"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

const (
	defaultPort        = 5432
	defaultStorageGB   = 32
	defaultStorageType = "Premium_LRS"
	defaultSKU         = "Standard_B1ms"
	defaultEngine      = "Postgres"
	armProvider        = "Microsoft.DBforPostgreSQL"
	armResourceType    = "flexibleServers"
	cpuMetricRunning   = 25.0 // percent
	cpuMetricStopped   = 0.0
	memMetricRunning   = 40.0
	storageMetric      = 25.0
	connRunning        = 5.0
)

var (
	_ rdsdriver.RelationalDB = (*Mock)(nil)
	_ rdsdriver.ScopedDelete = (*Mock)(nil)
)

// Mock is the in-memory Azure Postgres Flex implementation.
type Mock struct {
	mu sync.RWMutex

	instances *memstore.Store[rdsdriver.Instance]
	snapshots *memstore.Store[rdsdriver.Snapshot]

	databases      *memstore.Store[rdsdriver.Database]
	firewallRules  *memstore.Store[rdsdriver.FirewallRule]
	configurations *memstore.Store[rdsdriver.Configuration]

	// instSettle overlays a transient Starting / Updating window over a server's
	// stored available state so create/modify/restart report the real Azure
	// Postgres Flex ServerState before settling to Ready. A no-op unless
	// config.Options.AsyncSettle is set (SettleDuration returns 0 → inactive
	// window → the historical synchronous behavior). The Set carries its own lock.
	instSettle *settle.Set

	opts       *config.Options
	monitoring mondriver.Monitoring
}

// New creates a new Postgres Flex mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		instances:      memstore.New[rdsdriver.Instance](),
		snapshots:      memstore.New[rdsdriver.Snapshot](),
		databases:      memstore.New[rdsdriver.Database](),
		firewallRules:  memstore.New[rdsdriver.FirewallRule](),
		configurations: memstore.New[rdsdriver.Configuration](),
		instSettle:     settle.NewSet(),
		opts:           opts,
	}
}

// settleState overlays the server's settle window (if any) onto its stored
// terminal state: the transient value while the window is active and unelapsed,
// otherwise final. Absent window → final.
func (m *Mock) settleState(id, final string) string {
	return m.instSettle.State(id, m.opts.Clock.Now(), final)
}

// SetMonitoring wires an Azure-Monitor-style backend for auto-metric emission.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// emitServerMetrics emits Microsoft.DBforPostgreSQL/flexibleServers-shaped
// metrics. Real Azure Monitor exposes cpu_percent, memory_percent,
// storage_percent, and active_connections at the server scope.
func (m *Mock) emitServerMetrics(server string, cpu, conns float64) {
	if m.monitoring == nil {
		return
	}

	now := m.opts.Clock.Now()
	resourceID := flexibleServerResourceID(m.opts.Region, server)
	dims := map[string]string{"resourceId": resourceID}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{
		{Namespace: "Microsoft.DBforPostgreSQL/flexibleServers", MetricName: "cpu_percent",
			Value: cpu, Unit: "Percent", Dimensions: dims, Timestamp: now},
		{Namespace: "Microsoft.DBforPostgreSQL/flexibleServers", MetricName: "memory_percent",
			Value: memMetricRunning, Unit: "Percent", Dimensions: dims, Timestamp: now},
		{Namespace: "Microsoft.DBforPostgreSQL/flexibleServers", MetricName: "storage_percent",
			Value: storageMetric, Unit: "Percent", Dimensions: dims, Timestamp: now},
		{Namespace: "Microsoft.DBforPostgreSQL/flexibleServers", MetricName: "active_connections",
			Value: conns, Unit: "Count", Dimensions: dims, Timestamp: now},
	})
}

// flexibleServerResourceID constructs the canonical Azure resource ID for a
// flexible server. The region is reused as the subscription/resourceGroup
// stand-in so portable-API consumers get a well-formed ARN even without
// going through the SDK-compat HTTP layer.
func flexibleServerResourceID(region, server string) string {
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/%s/%s/%s",
		region, region, armProvider, armResourceType, server)
}

// flexibleServerEndpoint returns the public DNS endpoint Azure assigns to a
// Postgres Flex server.
func flexibleServerEndpoint(server string) string {
	return server + ".postgres.database.azure.com"
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

// CreateInstance creates a new Postgres Flex flexible server. The in-memory row
// is reserved under the provider lock, then the (potentially slow, cold-start)
// engine provisioning runs WITHOUT the lock so concurrent reads are never
// blocked, and only the resulting reachable host:port is written back afterward.
//
//nolint:gocritic // cfg matches the driver interface signature.
func (m *Mock) CreateInstance(ctx context.Context, cfg rdsdriver.InstanceConfig) (*rdsdriver.Instance, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "server name is required")
	}

	// Resolve the engine up front: Azure PostgreSQL Flex is always Postgres, and
	// dbengine matches the family off cfg.Engine (which may arrive empty).
	if cfg.Engine == "" {
		cfg.Engine = defaultEngine
	}

	inst, err := m.reserveInstance(cfg)
	if err != nil {
		return nil, err
	}

	if err := dbengine.Provision(ctx, m.opts.DatabaseEngine, &inst, &cfg); err != nil {
		m.rollbackReserved(cfg.ID)
		return nil, err
	}

	out := m.finalizeInstance(cfg.ID, inst)

	// Under AsyncSettle a fresh server reports Starting until the window elapses,
	// matching real Azure Postgres Flex (ServerState has no Creating; a
	// provisioning server reports Starting → Ready). Default off → SettleDuration
	// 0 → inactive window → Ready immediately.
	m.instSettle.Begin(cfg.ID, rdsdriver.StateCreating, m.opts.Clock.Now(),
		m.opts.SettleDuration(settle.DefaultAzureDBSettle))

	out.State = m.settleState(cfg.ID, out.State)

	m.emitServerMetrics(cfg.ID, cpuMetricRunning, connRunning)

	return &out, nil
}

// reserveInstance builds the server with its synthetic FQDN and stores it under
// the lock, returning a copy for provisioning.
//
//nolint:gocritic // cfg matches the driver interface signature.
func (m *Mock) reserveInstance(cfg rdsdriver.InstanceConfig) (rdsdriver.Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.instances.Get(cfg.ID); ok {
		return rdsdriver.Instance{}, cerrors.Newf(cerrors.AlreadyExists, "Postgres Flex server %q already exists", cfg.ID)
	}

	inst := m.newInstance(cfg)
	m.instances.Set(cfg.ID, inst)

	return inst, nil
}

// newInstance builds the server record with defaulted fields and the synthetic
// FQDN. The caller holds the lock.
//
//nolint:gocritic // cfg matches the driver interface signature.
func (m *Mock) newInstance(cfg rdsdriver.InstanceConfig) rdsdriver.Instance {
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

	sku := cfg.InstanceClass
	if sku == "" {
		sku = defaultSKU
	}

	region := cfg.Location
	if region == "" {
		region = m.opts.Region
	}

	return rdsdriver.Instance{
		ID:                      cfg.ID,
		ARN:                     flexibleServerResourceID(region, cfg.ID),
		Engine:                  cfg.Engine,
		EngineVersion:           cfg.EngineVersion,
		InstanceClass:           sku,
		AllocatedStorage:        storage,
		StorageType:             storageType,
		MasterUsername:          cfg.MasterUsername,
		DBName:                  cfg.DBName,
		Endpoint:                flexibleServerEndpoint(cfg.ID),
		Port:                    port,
		State:                   rdsdriver.StateAvailable,
		MultiAZ:                 cfg.MultiAZ || rdsdriver.HAEnabled(cfg.HighAvailabilityMode),
		PubliclyAccessible:      cfg.PubliclyAccessible,
		VPCSecurityGroups:       append([]string(nil), cfg.VPCSecurityGroups...),
		SubnetGroupName:         cfg.SubnetGroupName,
		Location:                region,
		AvailabilityZone:        cfg.AvailabilityZone,
		HighAvailabilityMode:    cfg.HighAvailabilityMode,
		StandbyAvailabilityZone: cfg.StandbyAvailabilityZone,
		CreatedAt:               m.opts.Clock.Now().UTC(),
		Tags:                    copyTags(cfg.Tags),
		Scope:                   cfg.Scope,
	}
}

// finalizeInstance writes the engine's reachable host:port back onto the reserved
// row under the lock and returns the finalized server copy.
//
//nolint:gocritic // inst is finalized and returned by value on purpose.
func (m *Mock) finalizeInstance(id string, inst rdsdriver.Instance) rdsdriver.Instance {
	m.mu.Lock()
	defer m.mu.Unlock()

	if stored, ok := m.instances.Get(id); ok {
		stored.Endpoint = inst.Endpoint
		stored.Port = inst.Port
		m.instances.Set(id, stored)
		inst = stored
	}

	return inst
}

// rollbackReserved removes the reserved row when provisioning fails.
func (m *Mock) rollbackReserved(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.instances.Delete(id)
}

// DescribeInstances returns all servers if ids is empty, else only matching ones.
func (m *Mock) DescribeInstances(_ context.Context, ids []string) ([]rdsdriver.Instance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(ids) == 0 {
		all := m.instances.All()
		out := make([]rdsdriver.Instance, 0, len(all))

		//nolint:gocritic // map values are large structs; copy is unavoidable when materializing the result slice.
		for _, v := range all {
			v.State = m.settleState(v.ID, v.State)
			out = append(out, v)
		}

		return out, nil
	}

	out := make([]rdsdriver.Instance, 0, len(ids))

	for _, id := range ids {
		inst, ok := m.instances.Get(id)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "Postgres Flex server %q not found", id)
		}

		inst.State = m.settleState(inst.ID, inst.State)
		out = append(out, inst)
	}

	return out, nil
}

// ModifyInstance applies the supplied changes.
//
//nolint:gocritic // input matches the driver interface signature.
func (m *Mock) ModifyInstance(
	ctx context.Context, id string, input rdsdriver.ModifyInstanceInput,
) (*rdsdriver.Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Postgres Flex server %q not found", id)
	}

	// Rotate the administrator password on the backing engine (a no-op when the
	// request carries none) so the new credential actually authenticates.
	if err := dbengine.RotatePassword(ctx, m.opts.DatabaseEngine, &inst, input.MasterUserPassword); err != nil {
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

	if input.MultiAZ != nil {
		inst.MultiAZ = *input.MultiAZ
	}

	if input.HighAvailabilityMode != "" {
		inst.HighAvailabilityMode = input.HighAvailabilityMode
		inst.MultiAZ = rdsdriver.HAEnabled(input.HighAvailabilityMode)
		// Disabling HA tears down the standby, so its zone no longer applies;
		// enabling it records the requested standby zone.
		if inst.MultiAZ {
			inst.StandbyAvailabilityZone = input.StandbyAvailabilityZone
		} else {
			inst.StandbyAvailabilityZone = ""
		}
	}

	if input.Tags != nil {
		inst.Tags = copyTags(input.Tags)
	}

	m.instances.Set(id, inst)

	// Under AsyncSettle a modified server briefly reports Updating before
	// settling back to Ready; a no-op when settle is off.
	m.instSettle.Begin(id, rdsdriver.StateModifying, m.opts.Clock.Now(),
		m.opts.SettleDuration(settle.DefaultAzureDBSettle))

	out := inst
	out.State = m.settleState(id, out.State)

	return &out, nil
}

// DeleteInstance removes a flexible server.
func (m *Mock) DeleteInstance(ctx context.Context, id string) error {
	return m.deleteInstanceScoped(ctx, id, scope.Scope{})
}

// DeleteInstanceInScope removes a flexible server, refusing (NotFound) when it
// exists but was created under a different subscription/resource group.
// Implements the optional rdsdriver.ScopedDelete capability so a cross-tenant
// DELETE through the ARM handler can never remove another scope's server.
func (m *Mock) DeleteInstanceInScope(ctx context.Context, id string, filter scope.Scope) error {
	return m.deleteInstanceScoped(ctx, id, filter)
}

// deleteInstanceScoped performs the get-check-delete under a single lock
// acquisition so the scope check and the delete are atomic. A zero filter
// matches any scope (see scope.Scope.Matches), so DeleteInstance's unscoped
// callers are unaffected.
func (m *Mock) deleteInstanceScoped(ctx context.Context, id string, filter scope.Scope) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances.Get(id)
	if !ok || !inst.Scope.Matches(filter) {
		return cerrors.Newf(cerrors.NotFound, "Postgres Flex server %q not found", id)
	}

	// Tear down the real database backing the server, if any.
	if err := dbengine.Deprovision(ctx, m.opts.DatabaseEngine, &inst); err != nil {
		return err
	}

	m.instances.Delete(id)
	m.instSettle.Clear(id)
	m.deleteChildren(id)

	return nil
}

// deleteChildren removes the databases, firewall rules and configurations that
// belong to server id, matching Azure's cascade delete on server removal. The
// caller already holds the write lock.
func (m *Mock) deleteChildren(server string) {
	prefix := server + "/"

	for key := range m.databases.All() {
		if strings.HasPrefix(key, prefix) {
			m.databases.Delete(key)
		}
	}

	for key := range m.firewallRules.All() {
		if strings.HasPrefix(key, prefix) {
			m.firewallRules.Delete(key)
		}
	}

	for key := range m.configurations.All() {
		if strings.HasPrefix(key, prefix) {
			m.configurations.Delete(key)
		}
	}
}

// StartInstance moves a stopped server back to running.
func (m *Mock) StartInstance(_ context.Context, id string) error {
	return m.transitionInstance(id, rdsdriver.StateStopped, rdsdriver.StateAvailable, cpuMetricRunning, connRunning, "start")
}

// StopInstance moves a running server to stopped.
func (m *Mock) StopInstance(_ context.Context, id string) error {
	return m.transitionInstance(id, rdsdriver.StateAvailable, rdsdriver.StateStopped, cpuMetricStopped, 0, "stop")
}

// RebootInstance cycles a running server through restart.
func (m *Mock) RebootInstance(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "Postgres Flex server %q not found", id)
	}

	if inst.State != rdsdriver.StateAvailable {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"Postgres Flex server %q is in state %q; restart requires %q", id, inst.State, rdsdriver.StateAvailable)
	}

	m.instances.Set(id, inst)

	// A restart briefly reports Updating (StateRebooting → Updating) before
	// settling back to Ready under AsyncSettle; a no-op when settle is off.
	m.instSettle.Begin(id, rdsdriver.StateRebooting, m.opts.Clock.Now(),
		m.opts.SettleDuration(settle.DefaultDBRebootSettle))

	m.emitServerMetrics(id, cpuMetricRunning, connRunning)

	return nil
}

func (m *Mock) transitionInstance(id, from, to string, cpu, conns float64, verb string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "Postgres Flex server %q not found", id)
	}

	if inst.State == to {
		return nil // idempotent
	}

	if inst.State != from {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"Postgres Flex server %q is in state %q; %s requires %q", id, inst.State, verb, from)
	}

	inst.State = to
	m.instances.Set(id, inst)

	// A start/stop transition supersedes any post-create settle window, so the
	// new terminal state is observed immediately.
	m.instSettle.Clear(id)

	m.emitServerMetrics(id, cpu, conns)

	return nil
}

// CreateCluster is unsupported on Postgres Flex — there is no cluster concept.
//
//nolint:gocritic // signature matches the driver interface.
func (*Mock) CreateCluster(_ context.Context, _ rdsdriver.ClusterConfig) (*rdsdriver.Cluster, error) {
	return nil, cerrors.New(cerrors.InvalidArgument,
		"Postgres Flex has no cluster concept; create flexible servers individually")
}

// DescribeClusters returns an empty list — Postgres Flex has no clusters.
func (*Mock) DescribeClusters(_ context.Context, _ []string) ([]rdsdriver.Cluster, error) {
	return []rdsdriver.Cluster{}, nil
}

// ModifyCluster is unsupported on Postgres Flex.
//
//nolint:gocritic // input matches the driver interface signature.
func (*Mock) ModifyCluster(
	_ context.Context, _ string, _ rdsdriver.ModifyInstanceInput,
) (*rdsdriver.Cluster, error) {
	return nil, cerrors.New(cerrors.InvalidArgument, "Postgres Flex has no cluster concept")
}

// DeleteCluster is unsupported on Postgres Flex.
func (*Mock) DeleteCluster(_ context.Context, _ string) error {
	return cerrors.New(cerrors.InvalidArgument, "Postgres Flex has no cluster concept")
}

// StartCluster is unsupported on Postgres Flex.
func (*Mock) StartCluster(_ context.Context, _ string) error {
	return cerrors.New(cerrors.InvalidArgument, "Postgres Flex has no cluster concept")
}

// StopCluster is unsupported on Postgres Flex.
func (*Mock) StopCluster(_ context.Context, _ string) error {
	return cerrors.New(cerrors.InvalidArgument, "Postgres Flex has no cluster concept")
}

// CreateSnapshot creates a synthetic backup of a flexible server. Real
// Postgres Flex backups are managed automatically; the portable API exposes
// them as snapshots so generic relationaldb consumers have a uniform
// snapshot/restore story.
func (m *Mock) CreateSnapshot(_ context.Context, cfg rdsdriver.SnapshotConfig) (*rdsdriver.Snapshot, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "snapshot id is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances.Get(cfg.InstanceID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Postgres Flex server %q not found", cfg.InstanceID)
	}

	if _, ok := m.snapshots.Get(cfg.ID); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "snapshot %q already exists", cfg.ID)
	}

	snap := rdsdriver.Snapshot{
		ID:               cfg.ID,
		ARN:              flexibleServerResourceID(m.opts.Region, cfg.InstanceID) + "/backups/" + cfg.ID,
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

// DeleteSnapshot removes a snapshot.
func (m *Mock) DeleteSnapshot(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.snapshots.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "snapshot %q not found", id)
	}

	return nil
}

// RestoreInstanceFromSnapshot creates a new flexible server from a snapshot.
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
		return nil, cerrors.Newf(cerrors.NotFound, "snapshot %q not found", input.SnapshotID)
	}

	if _, ok := m.instances.Get(input.NewInstanceID); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists,
			"Postgres Flex server %q already exists", input.NewInstanceID)
	}

	sku := input.InstanceClass
	if sku == "" {
		sku = defaultSKU
	}

	now := m.opts.Clock.Now().UTC()

	inst := rdsdriver.Instance{
		ID:               input.NewInstanceID,
		ARN:              flexibleServerResourceID(m.opts.Region, input.NewInstanceID),
		Engine:           snap.Engine,
		EngineVersion:    snap.EngineVersion,
		InstanceClass:    sku,
		AllocatedStorage: snap.AllocatedStorage,
		StorageType:      defaultStorageType,
		Endpoint:         flexibleServerEndpoint(input.NewInstanceID),
		Port:             defaultPort,
		State:            rdsdriver.StateAvailable,
		Location:         m.opts.Region,
		CreatedAt:        now,
		Tags:             copyTags(input.Tags),
	}

	m.instances.Set(input.NewInstanceID, inst)

	// A restore provisions a fresh server, so it settles Starting → Ready like a
	// create under AsyncSettle; a no-op when settle is off.
	m.instSettle.Begin(input.NewInstanceID, rdsdriver.StateCreating, m.opts.Clock.Now(),
		m.opts.SettleDuration(settle.DefaultAzureDBSettle))

	m.emitServerMetrics(input.NewInstanceID, cpuMetricRunning, connRunning)

	out := inst
	out.State = m.settleState(input.NewInstanceID, out.State)

	return &out, nil
}

// CreateClusterSnapshot is unsupported on Postgres Flex.
func (*Mock) CreateClusterSnapshot(
	_ context.Context, _ rdsdriver.ClusterSnapshotConfig,
) (*rdsdriver.ClusterSnapshot, error) {
	return nil, cerrors.New(cerrors.InvalidArgument, "Postgres Flex has no cluster concept")
}

// DescribeClusterSnapshots returns an empty list — Postgres Flex has no clusters.
func (*Mock) DescribeClusterSnapshots(
	_ context.Context, _ []string, _ string,
) ([]rdsdriver.ClusterSnapshot, error) {
	return []rdsdriver.ClusterSnapshot{}, nil
}

// DeleteClusterSnapshot is unsupported on Postgres Flex.
func (*Mock) DeleteClusterSnapshot(_ context.Context, _ string) error {
	return cerrors.New(cerrors.InvalidArgument, "Postgres Flex has no cluster concept")
}

// RestoreClusterFromSnapshot is unsupported on Postgres Flex.
func (*Mock) RestoreClusterFromSnapshot(
	_ context.Context, _ rdsdriver.RestoreClusterInput,
) (*rdsdriver.Cluster, error) {
	return nil, cerrors.New(cerrors.InvalidArgument, "Postgres Flex has no cluster concept")
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
