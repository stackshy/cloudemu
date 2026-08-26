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
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	dbengine "github.com/stackshy/cloudemu/v2/services/relationaldb/dbengine"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
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
	// syntheticPrivateIP is the ipAddresses[].ipAddress reported when no real
	// database engine backs the instance — preserving the historical behavior of
	// always surfacing an IP. When an engine is wired in, dbengine.Provision
	// overrides Endpoint with the real reachable host.
	syntheticPrivateIP = "10.0.0.1"
)

var _ rdsdriver.RelationalDB = (*Mock)(nil)

var _ rdsdriver.BackupRestorer = (*Mock)(nil)

// Mock is the in-memory GCP Cloud SQL implementation.
type Mock struct {
	mu sync.RWMutex

	instances *memstore.Store[rdsdriver.Instance]
	snapshots *memstore.Store[rdsdriver.Snapshot]

	// child resources keyed "instance/name" (sslCerts keyed "instance/sha1")
	databases *memstore.Store[rdsdriver.Database]
	users     *memstore.Store[rdsdriver.User]
	sslCerts  *memstore.Store[rdsdriver.SslCert]

	// backupSeq gives auto-generated backup-run IDs a deterministic,
	// collision-free suffix (guarded by mu).
	backupSeq int64

	// rootPasswords remembers each instance's master password (guarded by mu) so
	// a clone can re-provision its own database against a configured
	// DatabaseEngine. The emulator enforces no auth, so this is local state, not
	// a secret store; it is never logged.
	rootPasswords map[string]string

	opts       *config.Options
	monitoring mondriver.Monitoring
}

// New creates a new Cloud SQL mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		instances:     memstore.New[rdsdriver.Instance](),
		snapshots:     memstore.New[rdsdriver.Snapshot](),
		databases:     memstore.New[rdsdriver.Database](),
		users:         memstore.New[rdsdriver.User](),
		sslCerts:      memstore.New[rdsdriver.SslCert](),
		rootPasswords: map[string]string{},
		opts:          opts,
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

func cloneStrings(s []string) []string {
	if len(s) == 0 {
		return nil
	}

	return append([]string(nil), s...)
}

// cloneInstance / cloneSnapshot deep-copy the slice/map fields so a returned
// value never aliases the memstore — a caller mutating its result (or a
// concurrent reader) can't corrupt the store or trigger a concurrent-map
// read/write panic. Callers own the returned copy.
//
//nolint:gocritic // value copy is intentional — the result must not alias the store.
func cloneInstance(inst rdsdriver.Instance) rdsdriver.Instance {
	inst.Tags = copyTags(inst.Tags)
	inst.VPCSecurityGroups = cloneStrings(inst.VPCSecurityGroups)
	inst.ReadReplicaTargets = cloneStrings(inst.ReadReplicaTargets)

	return inst
}

//nolint:gocritic // value copy is intentional — the result must not alias the store.
func cloneSnapshot(s rdsdriver.Snapshot) rdsdriver.Snapshot {
	s.Tags = copyTags(s.Tags)

	return s
}

// CreateInstance creates a new Cloud SQL instance. The in-memory row is reserved
// under the provider lock, then the (potentially slow, cold-start) engine
// provisioning runs WITHOUT the lock so concurrent reads are never blocked, and
// only the resulting reachable host:port is written back under the lock.
//
//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) CreateInstance(ctx context.Context, cfg rdsdriver.InstanceConfig) (*rdsdriver.Instance, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "instance name is required")
	}

	if cfg.Engine == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "databaseVersion is required")
	}

	inst, err := m.reserveInstance(cfg)
	if err != nil {
		return nil, err
	}

	if err := dbengine.Provision(ctx, m.opts.DatabaseEngine, &inst, &cfg); err != nil {
		m.rollbackReserved(&inst)
		return nil, err
	}

	out := m.finalizeInstance(cfg.ID, inst)

	m.emitInstanceMetrics(cfg.ID, cpuMetricRunning, connRunning)

	return &out, nil
}

// reserveInstance builds the instance with its synthetic IP, links any replica
// relationship, and stores it under the lock, returning a copy for provisioning.
//
//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) reserveInstance(cfg rdsdriver.InstanceConfig) (rdsdriver.Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.instances.Get(cfg.ID); ok {
		return rdsdriver.Instance{}, cerrors.Newf(cerrors.AlreadyExists, "Cloud SQL instance %q already exists", cfg.ID)
	}

	inst := m.newInstance(cfg)

	if cfg.MasterInstanceName != "" {
		if err := m.linkReplica(&inst, cfg.MasterInstanceName); err != nil {
			return rdsdriver.Instance{}, err
		}
	}

	m.instances.Set(cfg.ID, inst)
	m.rootPasswords[cfg.ID] = cfg.MasterUserPassword

	return inst, nil
}

// newInstance builds the instance record with defaulted fields and the synthetic
// IP. The caller holds the lock.
//
//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) newInstance(cfg rdsdriver.InstanceConfig) rdsdriver.Instance {
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

	return rdsdriver.Instance{
		ID:               cfg.ID,
		ARN:              idgen.GCPID(m.opts.ProjectID, "instances", cfg.ID),
		Engine:           cfg.Engine,
		EngineVersion:    cfg.EngineVersion,
		InstanceClass:    tier,
		AllocatedStorage: storage,
		StorageType:      storageType,
		MasterUsername:   cfg.MasterUsername,
		DBName:           cfg.DBName,
		// ConnectionName carries the "project:region:id" identifier; Endpoint
		// carries the reachable host reported as the PRIMARY ipAddress. Without a
		// real engine this is a synthetic IP; dbengine.Provision overrides it with
		// the real host:port when one is wired in.
		ConnectionName:     instanceConnectionName(m.opts.ProjectID, region, cfg.ID),
		Endpoint:           syntheticPrivateIP,
		Port:               port,
		State:              rdsdriver.StateAvailable,
		MultiAZ:            cfg.MultiAZ,
		PubliclyAccessible: cfg.PubliclyAccessible,
		VPCSecurityGroups:  append([]string(nil), cfg.VPCSecurityGroups...),
		SubnetGroupName:    cfg.SubnetGroupName,
		AvailabilityZone:   region,
		CreatedAt:          m.opts.Clock.Now().UTC(),
		Tags:               copyTags(cfg.Tags),
		GCPDatabaseFlags:   cfg.GCPDatabaseFlags,
		GCPBackupConfig:    cfg.GCPBackupConfig,
		GCPIPConfig:        cfg.GCPIPConfig,
	}
}

// finalizeInstance writes the engine's reachable host:port back onto the reserved
// row under the lock and returns the finalized instance copy.
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

	return cloneInstance(inst)
}

// rollbackReserved undoes reserveInstance when provisioning fails: it removes the
// reserved row, its remembered password, and any replica links it created.
func (m *Mock) rollbackReserved(inst *rdsdriver.Instance) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.instances.Delete(inst.ID)
	delete(m.rootPasswords, inst.ID)
	m.unlinkReplicas(inst)
}

// linkReplica marks inst as a read replica of masterName and records it on the
// master's replica list. The caller holds the write lock.
func (m *Mock) linkReplica(inst *rdsdriver.Instance, masterName string) error {
	master, ok := m.instances.Get(masterName)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "master instance %q not found", masterName)
	}

	inst.ReadReplicaSource = masterName
	master.ReadReplicaTargets = append(append([]string(nil), master.ReadReplicaTargets...), inst.ID)
	m.instances.Set(masterName, master)

	return nil
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
			out = append(out, cloneInstance(v))
		}

		return out, nil
	}

	out := make([]rdsdriver.Instance, 0, len(ids))

	for _, id := range ids {
		inst, ok := m.instances.Get(id)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "Cloud SQL instance %q not found", id)
		}

		out = append(out, cloneInstance(inst))
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
		return nil, cerrors.Newf(cerrors.NotFound, "Cloud SQL instance %q not found", id)
	}

	// Rotate the master password on the backing engine so the new credential
	// actually authenticates.
	if input.MasterUserPassword != "" {
		if err := dbengine.RotatePassword(ctx, m.opts.DatabaseEngine, &inst, input.MasterUserPassword); err != nil {
			return nil, err
		}

		m.rootPasswords[id] = input.MasterUserPassword
	}

	if input.InstanceClass != "" {
		inst.InstanceClass = input.InstanceClass
	}

	if input.AllocatedStorage > 0 {
		inst.AllocatedStorage = input.AllocatedStorage
	}

	if input.EngineVersion != "" {
		// Cloud SQL's databaseVersion is stored in Engine (that is the field
		// toSQLInstance renders as databaseVersion), so a patched databaseVersion
		// must land there to be visible on the next Get.
		inst.Engine = input.EngineVersion
	}

	if input.MultiAZ != nil {
		inst.MultiAZ = *input.MultiAZ
	}

	applyGCPSettings(&inst, &input)

	if input.Tags != nil {
		inst.Tags = copyTags(input.Tags)
	}

	m.instances.Set(id, inst)

	out := cloneInstance(inst)

	return &out, nil
}

// applyGCPSettings merges the Cloud SQL settings sub-object blobs from a Patch
// onto the instance; an empty blob means "no change" (patch merges).
func applyGCPSettings(inst *rdsdriver.Instance, input *rdsdriver.ModifyInstanceInput) {
	if input.GCPDatabaseFlags != "" {
		inst.GCPDatabaseFlags = input.GCPDatabaseFlags
	}

	if input.GCPBackupConfig != "" {
		inst.GCPBackupConfig = input.GCPBackupConfig
	}

	if input.GCPIPConfig != "" {
		inst.GCPIPConfig = input.GCPIPConfig
	}
}

// DeleteInstance removes an instance, unlinks it from any replica relationship,
// and cascades to its children.
func (m *Mock) DeleteInstance(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "Cloud SQL instance %q not found", id)
	}

	// Tear down the real database backing the instance, if any.
	if err := dbengine.Deprovision(ctx, m.opts.DatabaseEngine, &inst); err != nil {
		return err
	}

	m.instances.Delete(id)
	delete(m.rootPasswords, id)
	m.unlinkReplicas(&inst)
	m.deleteChildren(id)

	return nil
}

// unlinkReplicas keeps the replica graph consistent when inst is removed: a
// deleted replica is dropped from its master's target list, and a deleted
// master's replicas have their source pointer cleared — so no surviving
// instance advertises a link to one that no longer exists. The caller holds the
// write lock.
func (m *Mock) unlinkReplicas(inst *rdsdriver.Instance) {
	if inst.ReadReplicaSource != "" {
		if master, ok := m.instances.Get(inst.ReadReplicaSource); ok {
			master.ReadReplicaTargets = removeStr(master.ReadReplicaTargets, inst.ID)
			m.instances.Set(inst.ReadReplicaSource, master)
		}
	}

	for _, replicaID := range inst.ReadReplicaTargets {
		if replica, ok := m.instances.Get(replicaID); ok {
			replica.ReadReplicaSource = ""
			m.instances.Set(replicaID, replica)
		}
	}
}

// deleteChildren removes the databases, users and SSL certs belonging to
// instance id. The caller already holds the write lock.
func (m *Mock) deleteChildren(instance string) {
	prefix := instance + "/"

	for key := range m.databases.All() {
		if strings.HasPrefix(key, prefix) {
			m.databases.Delete(key)
		}
	}

	for key := range m.users.All() {
		if strings.HasPrefix(key, prefix) {
			m.users.Delete(key)
		}
	}

	for key := range m.sslCerts.All() {
		if strings.HasPrefix(key, prefix) {
			m.sslCerts.Delete(key)
		}
	}
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
//
//nolint:gocritic // input matches the driver interface signature.
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
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances.Get(cfg.InstanceID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Cloud SQL instance %q not found", cfg.InstanceID)
	}

	// Cloud SQL generates the backup-run ID server-side; derive one
	// deterministically from the clock and a monotonic counter when the caller
	// omits it, so tests stay reproducible under a fake clock.
	id := cfg.ID
	if id == "" {
		m.backupSeq++
		id = fmt.Sprintf("%d", m.opts.Clock.Now().UnixNano()+m.backupSeq)
	}

	if _, ok := m.snapshots.Get(id); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "backup run %q already exists", id)
	}

	snap := rdsdriver.Snapshot{
		ID:               id,
		ARN:              idgen.GCPID(m.opts.ProjectID, "instances/"+cfg.InstanceID+"/backupRuns", id),
		InstanceID:       cfg.InstanceID,
		Engine:           inst.Engine,
		EngineVersion:    inst.EngineVersion,
		AllocatedStorage: inst.AllocatedStorage,
		State:            rdsdriver.SnapshotAvailable,
		CreatedAt:        m.opts.Clock.Now().UTC(),
		Tags:             copyTags(cfg.Tags),
	}

	m.snapshots.Set(id, snap)

	out := cloneSnapshot(snap)

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

		out = append(out, cloneSnapshot(snap))
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

// RestoreInstanceFromSnapshot creates a new instance from a backup run and backs
// it with a real database (when an engine is wired in) so the restored instance's
// reported IP is reachable, not a synthetic host that resolves to nothing.
func (m *Mock) RestoreInstanceFromSnapshot(
	ctx context.Context, input rdsdriver.RestoreInstanceInput,
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

	// Inherit the source instance's master login so the restored database is
	// reachable with the same credentials; fall back to engine defaults when the
	// source is gone.
	var username string
	if src, ok := m.instances.Get(snap.InstanceID); ok {
		username = src.MasterUsername
	}

	password := m.rootPasswords[snap.InstanceID]

	inst := rdsdriver.Instance{
		ID:               input.NewInstanceID,
		ARN:              idgen.GCPID(m.opts.ProjectID, "instances", input.NewInstanceID),
		Engine:           snap.Engine,
		EngineVersion:    snap.EngineVersion,
		InstanceClass:    tier,
		AllocatedStorage: snap.AllocatedStorage,
		StorageType:      defaultStorageType,
		MasterUsername:   username,
		ConnectionName:   instanceConnectionName(m.opts.ProjectID, m.opts.Region, input.NewInstanceID),
		Endpoint:         syntheticPrivateIP,
		Port:             defaultPortFor(snap.Engine),
		State:            rdsdriver.StateAvailable,
		AvailabilityZone: m.opts.Region,
		CreatedAt:        m.opts.Clock.Now().UTC(),
		Tags:             copyTags(input.Tags),
	}

	// Provision the restored instance's OWN database (keyed by the new id, so it
	// never aliases the source) through the same engine path CreateInstance uses.
	restoreCfg := rdsdriver.InstanceConfig{
		ID:                 input.NewInstanceID,
		Engine:             snap.Engine,
		MasterUsername:     username,
		MasterUserPassword: password,
	}
	if err := dbengine.Provision(ctx, m.opts.DatabaseEngine, &inst, &restoreCfg); err != nil {
		return nil, err
	}

	m.instances.Set(input.NewInstanceID, inst)
	m.rootPasswords[input.NewInstanceID] = password

	m.emitInstanceMetrics(input.NewInstanceID, cpuMetricRunning, connRunning)

	out := cloneInstance(inst)

	return &out, nil
}

// RestoreBackup restores a backup run in place onto an existing instance,
// matching Cloud SQL's restoreBackup semantics: the target instance must
// already exist and its engine/version/storage are overwritten from the
// backup. Unlike RestoreInstanceFromSnapshot it never provisions a new
// instance.
func (m *Mock) RestoreBackup(
	_ context.Context, targetInstanceID, backupRunID string,
) (*rdsdriver.Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	snap, ok := m.snapshots.Get(backupRunID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "backup run %q not found", backupRunID)
	}

	inst, ok := m.instances.Get(targetInstanceID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Cloud SQL instance %q not found", targetInstanceID)
	}

	inst.Engine = snap.Engine
	inst.EngineVersion = snap.EngineVersion
	inst.AllocatedStorage = snap.AllocatedStorage
	inst.State = rdsdriver.StateAvailable

	m.instances.Set(targetInstanceID, inst)
	m.emitInstanceMetrics(targetInstanceID, cpuMetricRunning, connRunning)

	out := cloneInstance(inst)

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
