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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	dbengine "github.com/stackshy/cloudemu/v2/services/relationaldb/dbengine"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

const (
	defaultPort          = 3306
	defaultPortPostgres  = 5432
	defaultPortNeptune   = 8182
	defaultPortDocDB     = 27017
	defaultStorage       = 20
	defaultStorageType   = "gp2"
	defaultInstanceClass = "db.t3.micro"
	cpuMetricRunning     = 25.0
	connectionsRunning   = 5.0
	cpuMetricStopped     = 0.0
)

// Defaults RDS applies to a new DBInstance/DBCluster that real AWS reports on
// read even when the caller did not set them.
const (
	defaultBackupRetention   = 1
	defaultCACertIdentifier  = "rds-ca-rsa2048-g1"
	defaultBackupWindow      = "07:00-07:30" //nolint:gosec // a maintenance-window time range, not a credential.
	defaultMaintenanceWindow = "sun:05:00-sun:05:30"
	defaultEngineMode        = "provisioned"
	auroraAllocatedStorage   = 1
	resourceIDLen            = 20
)

// Metric namespaces for engines that share the RDS wire protocol but emit
// CloudWatch metrics under their own service namespace in real AWS.
const (
	namespaceRDS     = "AWS/RDS"
	namespaceNeptune = "AWS/Neptune"
	namespaceDocDB   = "AWS/DocDB"
)

// engineNeptune and engineDocDB are the Engine values the AWS SDKs send for
// Amazon Neptune and Amazon DocumentDB. Both share the RDS query-protocol
// wire surface, so the same handler serves all three; only the port and the
// CloudWatch metric namespace differ by engine.
const (
	engineNeptune = "neptune"
	engineDocDB   = "docdb"
)

var _ rdsdriver.RelationalDB = (*Mock)(nil)

// clusterCred remembers an Aurora cluster's master credentials so a member
// instance (which carries none of its own) can be backed by the cluster's
// shared real database. The emulator enforces no auth, so this is local state,
// not a secret store; it is never logged.
type clusterCred struct {
	user string
	pass string
}

// Mock is the in-memory AWS RDS implementation.
type Mock struct {
	mu sync.RWMutex

	instances          *memstore.Store[rdsdriver.Instance]
	clusters           *memstore.Store[rdsdriver.Cluster]
	snapshots          *memstore.Store[rdsdriver.Snapshot]
	clusterSnapshots   *memstore.Store[rdsdriver.ClusterSnapshot]
	subnetGroups       *memstore.Store[rdsdriver.SubnetGroup]
	paramGroups        *memstore.Store[rdsdriver.ParameterGroup]
	clusterParamGroups *memstore.Store[rdsdriver.ClusterParameterGroup]
	optionGroups       *memstore.Store[rdsdriver.OptionGroup]
	proxies            *memstore.Store[rdsdriver.DBProxy]
	eventSubs          *memstore.Store[rdsdriver.EventSubscription]
	clusterEndpoints   *memstore.Store[rdsdriver.ClusterEndpoint]
	globalClusters     *memstore.Store[rdsdriver.GlobalCluster]

	// clusterCreds remembers each Aurora cluster's master credentials (guarded by
	// mu) so its member instances can provision the cluster's shared database.
	clusterCreds map[string]clusterCred

	// groupTags holds the tags for parameter/option/subnet groups, keyed by ARN
	// (guarded by mu). Those resources are taggable in real AWS but carry no tag
	// field on their records, so the tagging layer tracks them here rather than
	// answering 404 for a valid group ARN.
	groupTags map[string]map[string]string

	// rootPasswords remembers each standalone instance's master password (guarded
	// by mu) so a snapshot restore can re-provision a reachable database with the
	// original credentials, and a password rotation can be replayed. The emulator
	// enforces no auth, so this is local state, not a secret store; it is never
	// logged.
	rootPasswords map[string]string

	// instSettle / snapSettle overlay a "creating"/"rebooting" window over an
	// instance's or snapshot's stored (available) State on the Describe surface,
	// keyed by id and guarded by mu. They live beside the stores rather than on
	// the shared rdsdriver structs (which Azure flexible-server/SQL also use),
	// keeping settling AWS-internal. Absent = report the stored State directly.
	instSettle map[string]settle.Window
	snapSettle map[string]settle.Window

	opts           *config.Options
	subnetResolver SubnetResolver
	monitoring     mondriver.Monitoring
}

// New creates a new AWS RDS mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		instances:          memstore.New[rdsdriver.Instance](),
		clusters:           memstore.New[rdsdriver.Cluster](),
		snapshots:          memstore.New[rdsdriver.Snapshot](),
		clusterSnapshots:   memstore.New[rdsdriver.ClusterSnapshot](),
		subnetGroups:       memstore.New[rdsdriver.SubnetGroup](),
		paramGroups:        memstore.New[rdsdriver.ParameterGroup](),
		clusterParamGroups: memstore.New[rdsdriver.ClusterParameterGroup](),
		optionGroups:       memstore.New[rdsdriver.OptionGroup](),
		proxies:            memstore.New[rdsdriver.DBProxy](),
		eventSubs:          memstore.New[rdsdriver.EventSubscription](),
		clusterEndpoints:   memstore.New[rdsdriver.ClusterEndpoint](),
		globalClusters:     memstore.New[rdsdriver.GlobalCluster](),
		clusterCreds:       map[string]clusterCred{},
		groupTags:          map[string]map[string]string{},
		rootPasswords:      map[string]string{},
		instSettle:         map[string]settle.Window{},
		snapSettle:         map[string]settle.Window{},
		opts:               opts,
	}
}

// settleInstanceState overlays the instance's settle window (if any) onto its
// stored final state. The caller must hold at least m.mu.RLock.
func (m *Mock) settleInstanceState(id, final string) string {
	if w, ok := m.instSettle[id]; ok {
		return w.Observe(m.opts.Clock.Now(), final)
	}

	return final
}

// settleSnapshotState overlays the snapshot's settle window (if any) onto its
// stored final state. The caller must hold at least m.mu.RLock.
func (m *Mock) settleSnapshotState(id, final string) string {
	if w, ok := m.snapSettle[id]; ok {
		return w.Observe(m.opts.Clock.Now(), final)
	}

	return final
}

// SetMonitoring wires a CloudWatch-style backend for auto-metric emission.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

func (m *Mock) emitInstanceMetrics(instanceID, engine string, cpu, connections float64) {
	if m.monitoring == nil {
		return
	}

	now := m.opts.Clock.Now()
	dims := map[string]string{"DBInstanceIdentifier": instanceID}
	ns := namespaceFor(engine)

	// Latency and network throughput track whether the instance is serving
	// traffic; cpu is 0 only when the instance is stopped.
	running := cpu > 0

	var readLatency, writeLatency, rxThroughput, txThroughput float64
	if running {
		readLatency, writeLatency = 0.001, 0.002
		rxThroughput, txThroughput = 1<<20, 1<<19
	}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{
		{Namespace: ns, MetricName: "CPUUtilization", Value: cpu, Unit: "Percent", Dimensions: dims, Timestamp: now},
		{Namespace: ns, MetricName: "DatabaseConnections", Value: connections, Unit: "Count", Dimensions: dims, Timestamp: now},
		{Namespace: ns, MetricName: "FreeableMemory", Value: 1 << 30, Unit: "Bytes", Dimensions: dims, Timestamp: now},
		{Namespace: ns, MetricName: "FreeStorageSpace", Value: 10 << 30, Unit: "Bytes", Dimensions: dims, Timestamp: now},
		{Namespace: ns, MetricName: "ReadIOPS", Value: 10, Unit: "Count/Second", Dimensions: dims, Timestamp: now},
		{Namespace: ns, MetricName: "WriteIOPS", Value: 5, Unit: "Count/Second", Dimensions: dims, Timestamp: now},
		{Namespace: ns, MetricName: "ReadLatency", Value: readLatency, Unit: "Seconds", Dimensions: dims, Timestamp: now},
		{Namespace: ns, MetricName: "WriteLatency", Value: writeLatency, Unit: "Seconds", Dimensions: dims, Timestamp: now},
		{Namespace: ns, MetricName: "NetworkReceiveThroughput", Value: rxThroughput, Unit: "Bytes/Second", Dimensions: dims, Timestamp: now},
		{Namespace: ns, MetricName: "NetworkTransmitThroughput", Value: txThroughput, Unit: "Bytes/Second", Dimensions: dims, Timestamp: now},
	})
}

func defaultPortFor(engine string) int {
	switch engine {
	case "postgres", "aurora-postgresql":
		return defaultPortPostgres
	case engineNeptune:
		return defaultPortNeptune
	case engineDocDB:
		return defaultPortDocDB
	default:
		return defaultPort
	}
}

// namespaceFor returns the CloudWatch metric namespace for the given engine.
// Neptune and DocumentDB share the RDS wire protocol but real AWS emits their
// metrics under engine-specific namespaces, so we mirror that behavior.
func namespaceFor(engine string) string {
	switch engine {
	case engineNeptune:
		return namespaceNeptune
	case engineDocDB:
		return namespaceDocDB
	default:
		return namespaceRDS
	}
}

func endpointFor(id, region, kind string) string {
	return fmt.Sprintf("%s.%s.%s.rds.amazonaws.com", id, kind, region)
}

func instanceARN(region, accountID, id string) string {
	return idgen.AWSARN("rds", region, accountID, "db:"+id)
}

// resourceID derives a stable, region-unique RDS resource id (e.g.
// "db-ABCDEF...", "cluster-ABCDEF...") from a prefix and the resource id. Real
// AWS assigns an opaque immutable id; a deterministic hash keeps it stable
// across Describe calls without persisting a random value.
func resourceID(prefix, id string) string {
	sum := sha256.Sum256([]byte(id))
	return prefix + strings.ToUpper(hex.EncodeToString(sum[:])[:resourceIDLen])
}

func clusterARN(region, accountID, id string) string {
	return idgen.AWSARN("rds", region, accountID, "cluster:"+id)
}

// availabilityZones synthesizes the three AZ names AWS spreads an Aurora
// cluster across (region + a/b/c), matching the DBCluster AvailabilityZones
// array real RDS returns.
func availabilityZones(region string) []string {
	if region == "" {
		return nil
	}

	return []string{region + "a", region + "b", region + "c"}
}

func snapshotARN(region, accountID, id string) string {
	return idgen.AWSARN("rds", region, accountID, "snapshot:"+id)
}

func clusterSnapshotARN(region, accountID, id string) string {
	return idgen.AWSARN("rds", region, accountID, "cluster-snapshot:"+id)
}

// cloneSlice returns a shallow copy of s (nil for empty), so a slice returned
// by a Describe call never aliases the store's backing array — a caller
// mutating the result can't corrupt internal state.
func cloneSlice[T any](s []T) []T {
	if len(s) == 0 {
		return nil
	}

	return append([]T(nil), s...)
}

// cloneInstance / cloneCluster copy the map + slice fields so a value returned
// by a Describe call never aliases the store.
//
//nolint:gocritic // takes a value on purpose: it returns an independent copy.
func cloneInstance(in rdsdriver.Instance) rdsdriver.Instance {
	in.Tags = copyTags(in.Tags)
	in.VPCSecurityGroups = cloneSlice(in.VPCSecurityGroups)
	in.ReadReplicaTargets = cloneSlice(in.ReadReplicaTargets)

	return in
}

//nolint:gocritic // takes a value on purpose: it returns an independent copy.
func cloneCluster(c rdsdriver.Cluster) rdsdriver.Cluster {
	c.Tags = copyTags(c.Tags)
	c.Members = cloneSlice(c.Members)
	c.VPCSecurityGroups = cloneSlice(c.VPCSecurityGroups)
	c.AvailabilityZones = cloneSlice(c.AvailabilityZones)

	return c
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

// createPlan carries what the engine provision needs after the provider lock is
// released. For a cluster member it holds the cluster's shared engine key and
// credentials (a member carries none of its own); for a standalone instance it
// is the zero value and the instance's own cfg is used.
type createPlan struct {
	clusterMember bool
	engine        string
	dbName        string
	username      string
	password      string
}

// CreateInstance creates a new database instance. The in-memory row is reserved
// under the provider lock, then the (potentially slow, cold-start) engine
// provisioning runs WITHOUT the lock so concurrent reads are never blocked, and
// only the resulting reachable host:port is written back under the lock.
//
//nolint:gocritic // cfg matches the driver interface signature.
func (m *Mock) CreateInstance(ctx context.Context, cfg rdsdriver.InstanceConfig) (*rdsdriver.Instance, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "DBInstanceIdentifier is required")
	}

	if cfg.Engine == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "Engine is required")
	}

	if err := validateEngine(cfg.Engine); err != nil {
		return nil, err
	}

	if err := validateInstanceClass(cfg.InstanceClass); err != nil {
		return nil, err
	}

	inst, plan, err := m.reserveInstance(cfg)
	if err != nil {
		return nil, err
	}

	if err := m.runCreateProvision(ctx, &inst, &cfg, plan); err != nil {
		m.rollbackReserved(cfg.ID, cfg.ClusterID)
		return nil, err
	}

	out := m.finalizeInstance(cfg.ID, cfg.ClusterID, inst, plan)

	m.emitInstanceMetrics(cfg.ID, cfg.Engine, cpuMetricRunning, connectionsRunning)

	m.mu.RLock()
	out.State = m.settleInstanceState(cfg.ID, out.State)
	m.mu.RUnlock()

	return &out, nil
}

// reserveInstance validates, builds the instance with its synthetic endpoint,
// stores it (and, for a member, joins the cluster) under the lock, and returns
// the provisioning plan snapshotted while the lock is held.
//
//nolint:gocritic // cfg matches the driver interface signature.
func (m *Mock) reserveInstance(cfg rdsdriver.InstanceConfig) (rdsdriver.Instance, createPlan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.instances.Get(cfg.ID); ok {
		return rdsdriver.Instance{}, createPlan{}, cerrors.Newf(cerrors.AlreadyExists, "DB instance %q already exists", cfg.ID)
	}

	if cfg.ClusterID != "" {
		if _, ok := m.clusters.Get(cfg.ClusterID); !ok {
			return rdsdriver.Instance{}, createPlan{}, cerrors.Newf(cerrors.NotFound, "DB cluster %q not found", cfg.ClusterID)
		}
	}

	inst := m.newInstance(cfg)
	m.instances.Set(cfg.ID, inst)
	m.instSettle[cfg.ID] = settle.Pending(rdsdriver.StateCreating, m.opts.Clock.Now(),
		m.opts.SettleDuration(settle.DefaultDBInstanceSettle))
	m.rootPasswords[cfg.ID] = cfg.MasterUserPassword

	if cfg.ClusterID != "" {
		cluster, _ := m.clusters.Get(cfg.ClusterID)
		cluster.Members = append(cluster.Members, cfg.ID)
		m.clusters.Set(cfg.ClusterID, cluster)
	}

	return inst, m.planProvision(cfg), nil
}

// newInstance builds the instance record with defaulted fields and the synthetic
// endpoint. The caller holds the lock.
//
//nolint:gocritic // cfg matches the driver interface signature.
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

	instanceClass := cfg.InstanceClass
	if instanceClass == "" {
		instanceClass = defaultInstanceClass
	}

	engineVersion := cfg.EngineVersion
	if engineVersion == "" {
		engineVersion = defaultEngineVersion(cfg.Engine)
	}

	return rdsdriver.Instance{
		ID:                         cfg.ID,
		ARN:                        instanceARN(m.opts.Region, m.opts.AccountID, cfg.ID),
		Engine:                     cfg.Engine,
		EngineVersion:              engineVersion,
		InstanceClass:              instanceClass,
		AllocatedStorage:           storage,
		StorageType:                storageType,
		MasterUsername:             cfg.MasterUsername,
		DBName:                     cfg.DBName,
		Endpoint:                   endpointFor(cfg.ID, m.opts.Region, "abcd1234"),
		Port:                       port,
		State:                      rdsdriver.StateAvailable,
		MultiAZ:                    cfg.MultiAZ,
		PubliclyAccessible:         cfg.PubliclyAccessible,
		VPCSecurityGroups:          append([]string(nil), cfg.VPCSecurityGroups...),
		SubnetGroupName:            cfg.SubnetGroupName,
		DBParameterGroupName:       cfg.DBParameterGroupName,
		OptionGroupName:            cfg.OptionGroupName,
		ClusterID:                  cfg.ClusterID,
		AvailabilityZone:           cfg.AvailabilityZone,
		DbiResourceID:              resourceID("db-", cfg.ID),
		BackupRetentionPeriod:      defaultBackupRetention,
		PreferredBackupWindow:      defaultBackupWindow,
		PreferredMaintenanceWindow: defaultMaintenanceWindow,
		CACertificateIdentifier:    defaultCACertIdentifier,
		StorageEncrypted:           cfg.StorageEncrypted,
		CreatedAt:                  m.opts.Clock.Now().UTC(),
		Tags:                       copyTags(cfg.Tags),
	}
}

// planProvision snapshots, under the caller's lock, what runCreateProvision needs
// once the lock is released. An Aurora cluster member is backed by the cluster's
// shared database (keyed and named by the cluster ID) using the cluster's own
// credentials; a member whose cluster has no engine-backable family, or when no
// engine is wired in, provisions nothing and keeps its synthetic endpoint.
//
//nolint:gocritic // cfg matches the driver interface signature.
func (m *Mock) planProvision(cfg rdsdriver.InstanceConfig) createPlan {
	if cfg.ClusterID == "" {
		return createPlan{}
	}

	cluster, ok := m.clusters.Get(cfg.ClusterID)
	if !ok || m.opts.DatabaseEngine == nil ||
		(!dbengine.IsPostgresFamily(cluster.Engine) && !dbengine.IsMySQLFamily(cluster.Engine)) {
		return createPlan{}
	}

	creds := m.clusterCreds[cfg.ClusterID]

	return createPlan{
		clusterMember: true,
		engine:        cluster.Engine,
		dbName:        cfg.ClusterID,
		username:      creds.user,
		password:      creds.pass,
	}
}

// runCreateProvision backs the instance with the real engine WITHOUT holding the
// provider lock, mutating the caller's local copy's endpoint/port.
func (m *Mock) runCreateProvision(
	ctx context.Context, inst *rdsdriver.Instance, cfg *rdsdriver.InstanceConfig, plan createPlan,
) error {
	if plan.clusterMember {
		memberCfg := rdsdriver.InstanceConfig{
			ID:                 plan.dbName,
			Engine:             plan.engine,
			DBName:             plan.dbName,
			MasterUsername:     plan.username,
			MasterUserPassword: plan.password,
		}

		return dbengine.Provision(ctx, m.opts.DatabaseEngine, inst, &memberCfg)
	}

	return dbengine.Provision(ctx, m.opts.DatabaseEngine, inst, cfg)
}

// finalizeInstance writes the engine's reachable host:port back onto the reserved
// row (and, for a provisioned member, points the cluster endpoints at it) under
// the lock, returning the finalized instance.
//
//nolint:gocritic // inst is finalized and returned by value on purpose.
func (m *Mock) finalizeInstance(id, clusterID string, inst rdsdriver.Instance, plan createPlan) rdsdriver.Instance {
	m.mu.Lock()
	defer m.mu.Unlock()

	if stored, ok := m.instances.Get(id); ok {
		stored.Endpoint = inst.Endpoint
		stored.Port = inst.Port
		m.instances.Set(id, stored)
		inst = stored
	}

	if plan.clusterMember {
		if cluster, ok := m.clusters.Get(clusterID); ok {
			cluster.Endpoint = inst.Endpoint
			cluster.ReaderEndpoint = inst.Endpoint
			cluster.Port = inst.Port
			m.clusters.Set(clusterID, cluster)
		}
	}

	return inst
}

// rollbackReserved undoes reserveInstance when provisioning fails: it removes the
// reserved row, its remembered password, and any cluster membership.
func (m *Mock) rollbackReserved(id, clusterID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.instances.Delete(id)
	delete(m.rootPasswords, id)

	if clusterID != "" {
		if cluster, ok := m.clusters.Get(clusterID); ok {
			cluster.Members = removeString(cluster.Members, id)
			m.clusters.Set(clusterID, cluster)
		}
	}
}

// DescribeInstances returns all instances if ids is empty, else only matching ones.
func (m *Mock) DescribeInstances(_ context.Context, ids []string) ([]rdsdriver.Instance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(ids) == 0 {
		all := m.instances.All()
		out := make([]rdsdriver.Instance, 0, len(all))

		//nolint:gocritic // map values are large structs but we need a flat slice for the API.
		for _, v := range all {
			c := cloneInstance(v)
			c.State = m.settleInstanceState(c.ID, c.State)
			out = append(out, c)
		}

		return out, nil
	}

	out := make([]rdsdriver.Instance, 0, len(ids))

	for _, id := range ids {
		inst, ok := m.instances.Get(id)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "DB instance %q not found", id)
		}

		c := cloneInstance(inst)
		c.State = m.settleInstanceState(c.ID, c.State)
		out = append(out, c)
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
		return nil, cerrors.Newf(cerrors.NotFound, "DB instance %q not found", id)
	}

	// Reject an invalid DBInstanceClass before applying it, mirroring
	// CreateInstance. An empty class leaves the current one untouched.
	if err := validateInstanceClass(input.InstanceClass); err != nil {
		return nil, err
	}

	// Rotate the master password on the backing engine so the new credential
	// actually authenticates.
	if err := m.rotateEnginePassword(ctx, &inst, input.MasterUserPassword); err != nil {
		return nil, err
	}

	applyInstanceCoreMods(&inst, &input)
	applyInstanceStorageMods(&inst, &input)

	// A rename re-keys the store and rewrites the ARN + any replica references.
	// Apply the scalar changes first so the renamed row carries them.
	if renameRequested(id, &input) {
		return m.renameInstance(id, input.NewInstanceID, inst)
	}

	m.instances.Set(id, inst)

	out := inst

	return &out, nil
}

// renameRequested reports whether the input asks to rename the instance to a
// different identifier.
func renameRequested(id string, input *rdsdriver.ModifyInstanceInput) bool {
	return input.NewInstanceID != "" && input.NewInstanceID != id
}

// applyInstanceCoreMods applies the class/storage/version/param-group changes.
func applyInstanceCoreMods(inst *rdsdriver.Instance, input *rdsdriver.ModifyInstanceInput) {
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

	if input.DBParameterGroupName != "" {
		inst.DBParameterGroupName = input.DBParameterGroupName
	}

	if input.OptionGroupName != "" {
		inst.OptionGroupName = input.OptionGroupName
	}

	if input.Tags != nil {
		inst.Tags = copyTags(input.Tags)
	}
}

// applyInstanceStorageMods applies the storage/backup/maintenance/protection
// attributes real RDS honors on ModifyDBInstance.
func applyInstanceStorageMods(inst *rdsdriver.Instance, input *rdsdriver.ModifyInstanceInput) {
	if input.StorageType != "" {
		inst.StorageType = input.StorageType
	}

	if input.BackupRetentionPeriod > 0 {
		inst.BackupRetentionPeriod = input.BackupRetentionPeriod
	}

	if input.PreferredBackupWindow != "" {
		inst.PreferredBackupWindow = input.PreferredBackupWindow
	}

	if input.PreferredMaintenanceWindow != "" {
		inst.PreferredMaintenanceWindow = input.PreferredMaintenanceWindow
	}

	if input.Iops > 0 {
		inst.Iops = input.Iops
	}

	if input.DeletionProtection != nil {
		inst.DeletionProtection = *input.DeletionProtection
	}
}

// renameInstance re-keys an instance to newID under the caller's write lock:
// it rewrites the ARN, moves the remembered root password, updates the
// membership in any owning cluster, and fixes any replica source/target
// back-references so nothing dangles. It returns AlreadyExists if newID is
// taken.
//
//nolint:gocritic // inst is finalized and returned by value on purpose.
func (m *Mock) renameInstance(oldID, newID string, inst rdsdriver.Instance) (*rdsdriver.Instance, error) {
	if _, ok := m.instances.Get(newID); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "DB instance %q already exists", newID)
	}

	inst.ID = newID
	inst.ARN = instanceARN(m.opts.Region, m.opts.AccountID, newID)

	m.instances.Delete(oldID)
	m.instances.Set(newID, inst)

	if pw, ok := m.rootPasswords[oldID]; ok {
		delete(m.rootPasswords, oldID)
		m.rootPasswords[newID] = pw
	}

	if inst.ClusterID != "" {
		if cluster, ok := m.clusters.Get(inst.ClusterID); ok {
			cluster.Members = renameString(cluster.Members, oldID, newID)
			m.clusters.Set(inst.ClusterID, cluster)
		}
	}

	if inst.ReadReplicaSource != "" {
		if src, ok := m.instances.Get(inst.ReadReplicaSource); ok {
			src.ReadReplicaTargets = renameString(src.ReadReplicaTargets, oldID, newID)
			m.instances.Set(src.ID, src)
		}
	}

	for _, target := range inst.ReadReplicaTargets {
		if rep, ok := m.instances.Get(target); ok {
			rep.ReadReplicaSource = newID
			m.instances.Set(target, rep)
		}
	}

	out := inst

	return &out, nil
}

// rotateEnginePassword re-runs the engine role upsert so a new master password
// authenticates. A cluster member shares the cluster-owned database whose
// password is managed at the cluster scope, so instance-level rotation applies
// only to standalone instances; an empty password is a no-op.
func (m *Mock) rotateEnginePassword(ctx context.Context, inst *rdsdriver.Instance, newPassword string) error {
	if newPassword == "" || inst.ClusterID != "" {
		return nil
	}

	if err := dbengine.RotatePassword(ctx, m.opts.DatabaseEngine, inst, newPassword); err != nil {
		return err
	}

	m.rootPasswords[inst.ID] = newPassword

	return nil
}

// DeleteInstance removes an instance.
func (m *Mock) DeleteInstance(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "DB instance %q not found", id)
	}

	// Real AWS refuses to delete a source that still has read replicas; the
	// replicas would otherwise be orphaned with a dangling ReadReplicaSource.
	if len(inst.ReadReplicaTargets) > 0 {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"DB instance %q has read replicas %v; promote or delete them first", id, inst.ReadReplicaTargets)
	}

	if inst.ClusterID != "" {
		cluster, ok := m.clusters.Get(inst.ClusterID)
		if ok {
			cluster.Members = removeString(cluster.Members, id)
			m.clusters.Set(inst.ClusterID, cluster)
		}
	}

	// If this instance is itself a replica, detach it from its source so the
	// source no longer lists a replica that has gone.
	if inst.ReadReplicaSource != "" {
		if src, ok := m.instances.Get(inst.ReadReplicaSource); ok {
			src.ReadReplicaTargets = removeString(src.ReadReplicaTargets, id)
			m.instances.Set(src.ID, src)
		}
	}

	// Tear down the real database backing the instance, if any. A cluster member
	// shares the cluster-owned database (keyed by the cluster, not the member), so
	// it is left for DeleteCluster to tear down once — deleting one member must
	// not drop a database its siblings still use.
	if inst.ClusterID == "" {
		if err := dbengine.Deprovision(ctx, m.opts.DatabaseEngine, &inst); err != nil {
			return err
		}
	}

	m.instances.Delete(id)
	delete(m.rootPasswords, id)
	delete(m.instSettle, id)

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
	// The logical state stays available; a fresh "rebooting" window makes Describe
	// report the available->rebooting->available transient dip under AsyncSettle.
	m.instSettle[id] = settle.Pending(rdsdriver.StateRebooting, m.opts.Clock.Now(),
		m.opts.SettleDuration(settle.DefaultDBRebootSettle))

	m.emitInstanceMetrics(id, inst.Engine, cpuMetricRunning, connectionsRunning)

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
	// A lifecycle transition (start/stop) supersedes any post-create settle
	// window so the instance reports its new state, not a stale "creating".
	delete(m.instSettle, id)

	m.emitInstanceMetrics(id, inst.Engine, cpu, conns)

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

	engineMode := cfg.EngineMode
	if engineMode == "" {
		engineMode = defaultEngineMode
	}

	allocatedStorage := cfg.AllocatedStorage
	if allocatedStorage == 0 {
		allocatedStorage = auroraAllocatedStorage
	}

	cluster := rdsdriver.Cluster{
		ID:                          cfg.ID,
		ARN:                         clusterARN(m.opts.Region, m.opts.AccountID, cfg.ID),
		Engine:                      cfg.Engine,
		EngineVersion:               cfg.EngineVersion,
		MasterUsername:              cfg.MasterUsername,
		DatabaseName:                cfg.DatabaseName,
		Endpoint:                    endpointFor(cfg.ID, m.opts.Region, "cluster"),
		ReaderEndpoint:              endpointFor(cfg.ID, m.opts.Region, "cluster-ro"),
		Port:                        port,
		State:                       rdsdriver.StateAvailable,
		VPCSecurityGroups:           append([]string(nil), cfg.VPCSecurityGroups...),
		SubnetGroupName:             cfg.SubnetGroupName,
		DBClusterParameterGroupName: cfg.DBClusterParameterGroupName,
		EngineMode:                  engineMode,
		DBClusterResourceID:         resourceID("cluster-", cfg.ID),
		AllocatedStorage:            allocatedStorage,
		StorageEncrypted:            cfg.StorageEncrypted,
		DeletionProtection:          cfg.DeletionProtection,
		AvailabilityZones:           availabilityZones(m.opts.Region),
		CreatedAt:                   m.opts.Clock.Now().UTC(),
		Tags:                        copyTags(cfg.Tags),
	}

	m.clusters.Set(cfg.ID, cluster)
	m.clusterCreds[cfg.ID] = clusterCred{user: cfg.MasterUsername, pass: cfg.MasterUserPassword}

	out := cluster

	return &out, nil
}

// DescribeClusters returns all clusters if ids is empty, else only matching ones.
func (m *Mock) DescribeClusters(_ context.Context, ids []string) ([]rdsdriver.Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(ids) == 0 {
		all := m.clusters.All()
		out := make([]rdsdriver.Cluster, 0, len(all))

		//nolint:gocritic // map values are large structs but we need a flat slice for the API.
		for _, v := range all {
			out = append(out, cloneCluster(v))
		}

		return out, nil
	}

	out := make([]rdsdriver.Cluster, 0, len(ids))

	for _, id := range ids {
		cluster, ok := m.clusters.Get(id)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "DB cluster %q not found", id)
		}

		out = append(out, cloneCluster(cluster))
	}

	return out, nil
}

// ModifyCluster applies changes.
//
//nolint:gocritic // input matches the driver interface signature.
func (m *Mock) ModifyCluster(
	ctx context.Context, id string, input rdsdriver.ModifyInstanceInput,
) (*rdsdriver.Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cluster, ok := m.clusters.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "DB cluster %q not found", id)
	}

	// An Aurora cluster owns the shared database, so its master password is
	// rotated at the cluster scope (instance-level rotation skips members). Rotate
	// the shared engine role so the new credential AWS reports authenticates.
	if err := m.rotateClusterPassword(ctx, id, cluster.Engine, input.MasterUserPassword); err != nil {
		return nil, err
	}

	if input.EngineVersion != "" {
		cluster.EngineVersion = input.EngineVersion
	}

	if input.DBClusterParameterGroupName != "" {
		cluster.DBClusterParameterGroupName = input.DBClusterParameterGroupName
	}

	if input.DeletionProtection != nil {
		cluster.DeletionProtection = *input.DeletionProtection
	}

	if input.Tags != nil {
		cluster.Tags = copyTags(input.Tags)
	}

	m.clusters.Set(id, cluster)

	out := cluster

	return &out, nil
}

// rotateClusterPassword re-runs the shared engine role upsert for an Aurora
// cluster's cluster-scoped master password. An empty password is a no-op.
func (m *Mock) rotateClusterPassword(ctx context.Context, id, engine, newPassword string) error {
	if newPassword == "" {
		return nil
	}

	creds := m.clusterCreds[id]
	shared := rdsdriver.Instance{ID: id, Engine: engine, MasterUsername: creds.user}

	if err := dbengine.RotatePassword(ctx, m.opts.DatabaseEngine, &shared, newPassword); err != nil {
		return err
	}

	creds.pass = newPassword
	m.clusterCreds[id] = creds

	return nil
}

// DeleteCluster removes a cluster (only if it has no members).
func (m *Mock) DeleteCluster(ctx context.Context, id string) error {
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

	// Tear down the cluster's shared real database (provisioned by its members
	// under the cluster's own engine key), if any.
	shared := rdsdriver.Instance{ID: id, Engine: cluster.Engine}
	if err := dbengine.Deprovision(ctx, m.opts.DatabaseEngine, &shared); err != nil {
		return err
	}

	m.clusters.Delete(id)
	delete(m.clusterCreds, id)

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

	now := m.opts.Clock.Now()
	m.snapshots.Set(cfg.ID, snap)
	m.snapSettle[cfg.ID] = settle.Pending(rdsdriver.SnapshotCreating, now,
		m.opts.SettleDuration(settle.DefaultDBSnapshotSettle))
	// The source instance briefly reports "backing-up" while the snapshot settles,
	// matching real RDS; its logical state stays available.
	m.instSettle[cfg.InstanceID] = settle.Pending(rdsdriver.StateBackingUp, now,
		m.opts.SettleDuration(settle.DefaultDBSnapshotSettle))

	out := snap
	out.State = m.settleSnapshotState(cfg.ID, out.State)

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

		snap.Tags = copyTags(snap.Tags)
		snap.State = m.settleSnapshotState(snap.ID, snap.State)
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

	delete(m.snapSettle, id)

	return nil
}

// RestoreInstanceFromSnapshot creates a new instance from a snapshot and backs
// it with a real database (when an engine is wired in) so the restored endpoint
// is reachable, not a synthetic host that resolves to nothing.
func (m *Mock) RestoreInstanceFromSnapshot(
	ctx context.Context, input rdsdriver.RestoreInstanceInput,
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

	// Inherit the source instance's master login so the restored database is
	// reachable with the same credentials; fall back to engine defaults when the
	// source is gone. The password is remembered under the snapshot's source id.
	var username string
	if src, ok := m.instances.Get(snap.InstanceID); ok {
		username = src.MasterUsername
	}

	password := m.rootPasswords[snap.InstanceID]

	inst := rdsdriver.Instance{
		ID:               input.NewInstanceID,
		ARN:              instanceARN(m.opts.Region, m.opts.AccountID, input.NewInstanceID),
		Engine:           snap.Engine,
		EngineVersion:    snap.EngineVersion,
		InstanceClass:    instanceClass,
		AllocatedStorage: snap.AllocatedStorage,
		StorageType:      defaultStorageType,
		MasterUsername:   username,
		Endpoint:         endpointFor(input.NewInstanceID, m.opts.Region, "abcd1234"),
		Port:             defaultPortFor(snap.Engine),
		State:            rdsdriver.StateAvailable,
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

	m.emitInstanceMetrics(input.NewInstanceID, snap.Engine, cpuMetricRunning, connectionsRunning)

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

		snap.Tags = copyTags(snap.Tags)
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

// RestoreClusterFromSnapshot creates a new cluster from a cluster snapshot and
// provisions its shared real database (when an engine is wired in) so the
// reported endpoints — and any members added later — reach a real database.
func (m *Mock) RestoreClusterFromSnapshot(
	ctx context.Context, input rdsdriver.RestoreClusterInput,
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

	// Inherit the source cluster's master credentials so the restored shared
	// database is reachable and future members provision consistently.
	creds := m.clusterCreds[snap.ClusterID]

	cluster := rdsdriver.Cluster{
		ID:             input.NewClusterID,
		ARN:            clusterARN(m.opts.Region, m.opts.AccountID, input.NewClusterID),
		Engine:         snap.Engine,
		EngineVersion:  snap.EngineVersion,
		MasterUsername: creds.user,
		Endpoint:       endpointFor(input.NewClusterID, m.opts.Region, "cluster"),
		ReaderEndpoint: endpointFor(input.NewClusterID, m.opts.Region, "cluster-ro"),
		Port:           defaultPortFor(snap.Engine),
		State:          rdsdriver.StateAvailable,
		CreatedAt:      m.opts.Clock.Now().UTC(),
		Tags:           copyTags(input.Tags),
	}

	// Provision the shared database keyed and named by the new cluster id (an
	// Aurora cluster's members share ONE database), through the same engine path
	// CreateInstance uses. A no-engine or non-backable family keeps the synthetic
	// endpoints untouched.
	shared := rdsdriver.Instance{ID: input.NewClusterID, Engine: snap.Engine}
	sharedCfg := rdsdriver.InstanceConfig{
		ID:                 input.NewClusterID,
		Engine:             snap.Engine,
		DBName:             input.NewClusterID,
		MasterUsername:     creds.user,
		MasterUserPassword: creds.pass,
	}

	if err := dbengine.Provision(ctx, m.opts.DatabaseEngine, &shared, &sharedCfg); err != nil {
		return nil, err
	}

	if shared.Endpoint != "" {
		cluster.Endpoint = shared.Endpoint
		cluster.ReaderEndpoint = shared.Endpoint
		cluster.Port = shared.Port
	}

	m.clusters.Set(input.NewClusterID, cluster)
	m.clusterCreds[input.NewClusterID] = clusterCred{user: creds.user, pass: creds.pass}

	out := cluster

	return &out, nil
}

// removeString returns a NEW slice with target removed. It never mutates the
// input's backing array, so a slice previously handed out by a Describe call
// is never corrupted by a later membership change.
func removeString(slice []string, target string) []string {
	out := make([]string, 0, len(slice))

	for _, v := range slice {
		if v != target {
			out = append(out, v)
		}
	}

	return out
}

// renameString returns a NEW slice with every occurrence of oldVal replaced by
// newVal, never mutating the input's backing array.
func renameString(slice []string, oldVal, newVal string) []string {
	out := make([]string, len(slice))

	for i, v := range slice {
		if v == oldVal {
			out[i] = newVal
		} else {
			out[i] = v
		}
	}

	return out
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
