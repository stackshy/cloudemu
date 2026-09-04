// Package redshift provides an in-memory mock of AWS Redshift. It implements
// relationaldb/driver.RelationalDB so the same backend serves both the
// portable API (relationaldb.DB) and the SDK-compat HTTP layer.
//
// Redshift's primary unit is the cluster — there is no separate "instance"
// resource. Instance-level operations therefore return InvalidArgument with a
// hint to use the cluster ops; cluster lifecycle (Create/Modify/Delete/Reboot)
// and cluster snapshot/restore are first-class. The mock emits CloudWatch-shaped
// AWS/Redshift metrics on cluster lifecycle transitions to mirror real Redshift.
package redshift

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/relationaldb/dbengine"
	rdbdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

const (
	defaultEngine   = "redshift"
	defaultPort     = 5439
	singleNodeCount = 1
	// defaultKMSKeyAlias is the account-default Redshift KMS key real AWS fills in
	// when a cluster is created encrypted without an explicit KmsKeyId.
	defaultKMSKeyAlias       = "alias/aws/redshift"
	snapshotBackupSizeMB     = 100.0
	cpuUtilizationRunning    = 25.0
	databaseConnectionsRun   = 5.0
	readIOPSRunning          = 10.0
	writeIOPSRunning         = 5.0
	networkReceiveThroughput = 1024.0
)

// errInstanceOpsUnsupported is the canonical error returned for instance-level
// operations, which Redshift does not model.
var errInstanceOpsUnsupported = cerrors.New(cerrors.InvalidArgument,
	"Redshift uses clusters; use cluster operations instead of instance operations")

var _ rdbdriver.RelationalDB = (*Mock)(nil)

// ParameterGroup and SubnetGroup are lightweight redshift-specific resources
// (not part of the shared relationaldb driver). The emulator stores their
// identity so IaC that creates and references them succeeds.
type ParameterGroup struct {
	Name        string
	Family      string
	Description string
	// Parameters holds the group's engine parameters keyed by name. A freshly
	// created group is seeded with the family's engine-default values; a
	// ModifyClusterParameterGroup override flips a parameter's Source to "user".
	Parameters map[string]rdbdriver.Parameter
}

type SubnetGroup struct {
	Name        string
	Description string
	SubnetIDs   []string
	// VPCID is derived from the member subnets (real Redshift infers it rather
	// than taking it as input); empty when no subnet resolver is wired in.
	VPCID string
	// Subnets carries each member subnet with its availability zone, resolved at
	// create time, so DescribeClusterSubnetGroups can emit the full Subnets list.
	Subnets []Subnet
}

// Subnet is a member subnet of a cluster subnet group with its availability
// zone, mirroring the AWS Redshift Subnet shape returned on describe.
type Subnet struct {
	ID               string
	AvailabilityZone string
}

// Mock is the in-memory AWS Redshift implementation.
type Mock struct {
	mu sync.RWMutex

	clusters         *memstore.Store[rdbdriver.Cluster]
	clusterSnapshots *memstore.Store[rdbdriver.ClusterSnapshot]
	parameterGroups  *memstore.Store[ParameterGroup]
	subnetGroups     *memstore.Store[SubnetGroup]
	tagsByARN        map[string]map[string]string // ResourceName (ARN) -> tags

	// clusterSettle overlays a transient creating/modifying window (keyed by
	// cluster id) over a cluster's stored "available" state so create/modify report
	// the real Redshift intermediate state before settling. It is a no-op unless
	// config.Options.AsyncSettle is set (SettleDuration returns 0 → inactive window
	// → historical synchronous behavior). The Set has its own lock.
	clusterSettle *settle.Set

	opts           *config.Options
	monitoring     mondriver.Monitoring
	subnetResolver SubnetResolver
}

// New creates a new AWS Redshift mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		clusters:         memstore.New[rdbdriver.Cluster](),
		clusterSnapshots: memstore.New[rdbdriver.ClusterSnapshot](),
		parameterGroups:  memstore.New[ParameterGroup](),
		subnetGroups:     memstore.New[SubnetGroup](),
		clusterSettle:    settle.NewSet(),
		opts:             opts,
	}
}

// settleClusterState overlays a cluster's settle window (keyed by id) onto its
// stored terminal state: the transient value while the window is active and
// unelapsed, otherwise final. Absent window → final.
func (m *Mock) settleClusterState(id, final string) string {
	return m.clusterSettle.State(id, m.opts.Clock.Now(), final)
}

// CreateClusterParameterGroup registers a redshift cluster parameter group.
func (m *Mock) CreateClusterParameterGroup(_ context.Context, name, family, description string) (*ParameterGroup, error) {
	if name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "parameter group name is required")
	}

	if m.parameterGroups.Has(name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "parameter group %q already exists", name)
	}

	pg := ParameterGroup{
		Name:        name,
		Family:      family,
		Description: description,
		Parameters:  defaultRedshiftParameters(),
	}
	m.parameterGroups.Set(name, pg)

	return &pg, nil
}

// ModifyClusterParameterGroup applies parameter overrides to a group. Each
// modified parameter's Source becomes "user". An unknown parameter name is an
// InvalidArgument (mapped to InvalidParameterValue), matching real Redshift's
// rejection of parameters not in the group's family.
func (m *Mock) ModifyClusterParameterGroup(
	_ context.Context, name string, params []rdbdriver.Parameter,
) (*ParameterGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pg, ok := m.parameterGroups.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "parameter group %q not found", name)
	}

	if pg.Parameters == nil {
		pg.Parameters = defaultRedshiftParameters()
	}

	// Validate every parameter before applying any, so a bad entry never leaves
	// earlier ones half-applied.
	for i := range params {
		if _, known := pg.Parameters[params[i].Name]; !known {
			return nil, cerrors.Newf(cerrors.InvalidArgument,
				"could not find parameter with name: %s", params[i].Name)
		}
	}

	for i := range params {
		cur := pg.Parameters[params[i].Name]
		cur.Value = params[i].Value
		cur.Source = "user"
		pg.Parameters[params[i].Name] = cur
	}

	m.parameterGroups.Set(name, pg)

	out := pg

	return &out, nil
}

// DescribeClusterParameters returns the parameters of a group, sorted by name.
func (m *Mock) DescribeClusterParameters(_ context.Context, name string) ([]rdbdriver.Parameter, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pg, ok := m.parameterGroups.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "parameter group %q not found", name)
	}

	params := pg.Parameters
	if params == nil {
		params = defaultRedshiftParameters()
	}

	names := make([]string, 0, len(params))
	for n := range params {
		names = append(names, n)
	}

	sort.Strings(names)

	out := make([]rdbdriver.Parameter, 0, len(names))
	for _, n := range names {
		out = append(out, params[n])
	}

	return out, nil
}

// ResetClusterParameterGroup restores parameters to their engine defaults —
// the named ones, or all of them when resetAll is set.
func (m *Mock) ResetClusterParameterGroup(
	_ context.Context, name string, paramNames []string, resetAll bool,
) (*ParameterGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pg, ok := m.parameterGroups.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "parameter group %q not found", name)
	}

	defaults := defaultRedshiftParameters()

	if resetAll {
		pg.Parameters = defaults
		m.parameterGroups.Set(name, pg)

		out := pg

		return &out, nil
	}

	if pg.Parameters == nil {
		pg.Parameters = defaultRedshiftParameters()
	}

	for _, n := range paramNames {
		if def, known := defaults[n]; known {
			pg.Parameters[n] = def
		}
	}

	m.parameterGroups.Set(name, pg)

	out := pg

	return &out, nil
}

// DescribeClusterParameterGroups returns the named parameter groups, or all of
// them when names is empty. An unknown name is a NotFound error, matching AWS.
func (m *Mock) DescribeClusterParameterGroups(_ context.Context, names []string) ([]ParameterGroup, error) {
	if len(names) == 0 {
		return m.parameterGroups.SortedValues(), nil
	}

	out := make([]ParameterGroup, 0, len(names))

	for _, name := range names {
		pg, ok := m.parameterGroups.Get(name)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "parameter group %q not found", name)
		}

		out = append(out, pg)
	}

	return out, nil
}

// DeleteClusterParameterGroup removes a redshift cluster parameter group. Real
// Redshift refuses while any cluster still references the group
// (InvalidClusterParameterGroupStateFault); deleting it out from under a live
// cluster would strand that cluster's configuration. The in-use scan and the
// delete run under one lock so a concurrent CreateCluster cannot slip a
// referencing cluster in between them.
func (m *Mock) DeleteClusterParameterGroup(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if user, ok := m.clusterParameterGroupInUseBy(name); ok {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"cluster parameter group %q is in use by cluster %q", name, user)
	}

	if !m.parameterGroups.Delete(name) {
		return cerrors.Newf(cerrors.NotFound, "parameter group %q not found", name)
	}

	return nil
}

// clusterParameterGroupInUseBy names a cluster still referencing the given
// parameter group, if any. Callers must hold m.mu.
func (m *Mock) clusterParameterGroupInUseBy(name string) (string, bool) {
	clusters := m.clusters.All()
	for id := range clusters {
		if clusters[id].DBClusterParameterGroupName == name {
			return id, true
		}
	}

	return "", false
}

// CreateClusterSubnetGroup registers a redshift cluster subnet group.
func (m *Mock) CreateClusterSubnetGroup(ctx context.Context, name, description string, subnetIDs []string) (*SubnetGroup, error) {
	if name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "subnet group name is required")
	}

	if m.subnetGroups.Has(name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "subnet group %q already exists", name)
	}

	vpcID, subnets := m.resolveSubnets(ctx, subnetIDs)

	sg := SubnetGroup{
		Name:        name,
		Description: description,
		SubnetIDs:   append([]string(nil), subnetIDs...),
		VPCID:       vpcID,
		Subnets:     subnets,
	}
	m.subnetGroups.Set(name, sg)

	return &sg, nil
}

// DescribeClusterSubnetGroups returns the named subnet groups, or all of them
// when names is empty. An unknown name is a NotFound error, matching AWS.
func (m *Mock) DescribeClusterSubnetGroups(_ context.Context, names []string) ([]SubnetGroup, error) {
	if len(names) == 0 {
		return m.subnetGroups.SortedValues(), nil
	}

	out := make([]SubnetGroup, 0, len(names))

	for _, name := range names {
		sg, ok := m.subnetGroups.Get(name)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "subnet group %q not found", name)
		}

		out = append(out, sg)
	}

	return out, nil
}

// DeleteClusterSubnetGroup removes a redshift cluster subnet group. Real
// Redshift refuses while any cluster is still placed in the group
// (ClusterSubnetGroupInUseFault); deleting it out from under a live cluster
// would strand that cluster in a group that no longer exists. The in-use scan
// and the delete run under one lock so a concurrent CreateCluster cannot slip a
// referencing cluster in between them.
func (m *Mock) DeleteClusterSubnetGroup(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if user, ok := m.clusterSubnetGroupInUseBy(name); ok {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"cluster subnet group %q is in use by cluster %q", name, user)
	}

	if !m.subnetGroups.Delete(name) {
		return cerrors.Newf(cerrors.NotFound, "subnet group %q not found", name)
	}

	return nil
}

// clusterSubnetGroupInUseBy names a cluster still placed in the given subnet
// group, if any. Callers must hold m.mu.
func (m *Mock) clusterSubnetGroupInUseBy(name string) (string, bool) {
	clusters := m.clusters.All()
	for id := range clusters {
		if clusters[id].SubnetGroupName == name {
			return id, true
		}
	}

	return "", false
}

// SetMonitoring wires a CloudWatch-style backend for auto-metric emission.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

func (m *Mock) emitClusterMetrics(clusterID string, cpu, conns, readIOPS, writeIOPS, netRx float64) {
	if m.monitoring == nil {
		return
	}

	now := m.opts.Clock.Now()
	dims := map[string]string{"ClusterIdentifier": clusterID}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{
		{Namespace: "AWS/Redshift", MetricName: "CPUUtilization", Value: cpu, Unit: "Percent", Dimensions: dims, Timestamp: now},
		{
			Namespace: "AWS/Redshift", MetricName: "DatabaseConnections", Value: conns,
			Unit: "Count", Dimensions: dims, Timestamp: now,
		},
		{Namespace: "AWS/Redshift", MetricName: "ReadIOPS", Value: readIOPS, Unit: "Count/Second", Dimensions: dims, Timestamp: now},
		{Namespace: "AWS/Redshift", MetricName: "WriteIOPS", Value: writeIOPS, Unit: "Count/Second", Dimensions: dims, Timestamp: now},
		{
			Namespace: "AWS/Redshift", MetricName: "NetworkReceiveThroughput", Value: netRx,
			Unit: "Bytes/Second", Dimensions: dims, Timestamp: now,
		},
	})
}

func endpointFor(id string) string {
	return fmt.Sprintf("%s.redshift.amazonaws.com", id)
}

func clusterARN(region, accountID, id string) string {
	return idgen.AWSARN("redshift", region, accountID, "cluster:"+id)
}

func clusterSnapshotARN(region, accountID, id string) string {
	return idgen.AWSARN("redshift", region, accountID, "snapshot:"+id)
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

// CreateInstance is unsupported in Redshift.
//
//nolint:gocritic // cfg matches the driver interface signature.
func (*Mock) CreateInstance(_ context.Context, _ rdbdriver.InstanceConfig) (*rdbdriver.Instance, error) {
	return nil, errInstanceOpsUnsupported
}

// DescribeInstances is unsupported in Redshift.
func (*Mock) DescribeInstances(_ context.Context, _ []string) ([]rdbdriver.Instance, error) {
	return nil, errInstanceOpsUnsupported
}

// ModifyInstance is unsupported in Redshift.
func (*Mock) ModifyInstance(
	_ context.Context, _ string, _ rdbdriver.ModifyInstanceInput,
) (*rdbdriver.Instance, error) {
	return nil, errInstanceOpsUnsupported
}

// DeleteInstance is unsupported in Redshift.
func (*Mock) DeleteInstance(_ context.Context, _ string) error {
	return errInstanceOpsUnsupported
}

// StartInstance is unsupported in Redshift.
func (*Mock) StartInstance(_ context.Context, _ string) error {
	return errInstanceOpsUnsupported
}

// StopInstance is unsupported in Redshift.
func (*Mock) StopInstance(_ context.Context, _ string) error {
	return errInstanceOpsUnsupported
}

// RebootInstance delegates to RebootCluster — Redshift only has clusters, so a
// "reboot instance" call against a Redshift cluster ID is interpreted as a
// cluster reboot.
func (m *Mock) RebootInstance(ctx context.Context, id string) error {
	return m.RebootCluster(ctx, id)
}

// CreateCluster creates a new Redshift cluster. When a real DatabaseEngine is
// wired in, Redshift (a Postgres-wire service) is backed by the shared Postgres
// engine keyed by the cluster ID, and the synthetic endpoint is overridden with
// the real host:port a client connects to.
//
//nolint:gocritic // cfg matches the driver interface signature.
func (m *Mock) CreateCluster(ctx context.Context, cfg rdbdriver.ClusterConfig) (*rdbdriver.Cluster, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "ClusterIdentifier is required")
	}

	cluster, err := m.reserveCluster(cfg)
	if err != nil {
		return nil, err
	}

	// Engine wired in: provision the real database WITHOUT the lock, then write the
	// reachable host:port back onto the stored row under a re-acquired lock.
	if m.opts.DatabaseEngine != nil {
		if err := m.provisionEngine(ctx, &cluster, cfg); err != nil {
			m.rollbackCluster(cfg.ID)

			return nil, err
		}

		m.finalizeCluster(cluster.ID, cluster.Endpoint, cluster.Port)
	}

	m.emitClusterMetrics(cfg.ID, cpuUtilizationRunning, databaseConnectionsRun,
		readIOPSRunning, writeIOPSRunning, networkReceiveThroughput)

	// Under AsyncSettle a fresh cluster reports creating until the window elapses,
	// matching real Redshift (CreateCluster → creating → available). With the
	// default (AsyncSettle off) SettleDuration is 0 → inactive window → available
	// immediately, so nothing changes for existing callers.
	m.clusterSettle.Begin(cfg.ID, rdbdriver.StateCreating, m.opts.Clock.Now(),
		m.opts.SettleDuration(settle.DefaultClusterSettle))

	out := cluster
	out.State = m.settleClusterState(cfg.ID, out.State)

	return &out, nil
}

// reserveCluster builds the cluster with its synthetic endpoint and stores it
// under the provider lock, returning the reserved row.
//
//nolint:gocritic // cfg matches the driver interface signature.
func (m *Mock) reserveCluster(cfg rdbdriver.ClusterConfig) (rdbdriver.Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.clusters.Get(cfg.ID); ok {
		return rdbdriver.Cluster{}, cerrors.Newf(cerrors.AlreadyExists, "Redshift cluster %q already exists", cfg.ID)
	}

	// A referenced subnet group must exist, matching real Redshift's
	// ClusterSubnetGroupNotFoundFault (the handler maps "subnet group" NotFound
	// to that code); otherwise IaC ordering mistakes are silently masked.
	if cfg.SubnetGroupName != "" && !m.subnetGroups.Has(cfg.SubnetGroupName) {
		return rdbdriver.Cluster{}, cerrors.Newf(cerrors.NotFound,
			"cluster subnet group %q not found", cfg.SubnetGroupName)
	}

	engine := cfg.Engine
	if engine == "" {
		engine = defaultEngine
	}

	port := cfg.Port
	if port == 0 {
		port = defaultPort
	}

	numberOfNodes := cfg.NumberOfNodes
	if numberOfNodes == 0 {
		numberOfNodes = singleNodeCount
	}

	cluster := rdbdriver.Cluster{
		ID:                          cfg.ID,
		ARN:                         clusterARN(m.opts.Region, m.opts.AccountID, cfg.ID),
		Engine:                      engine,
		EngineVersion:               cfg.EngineVersion,
		MasterUsername:              cfg.MasterUsername,
		DatabaseName:                cfg.DatabaseName,
		Endpoint:                    endpointFor(cfg.ID),
		Port:                        port,
		State:                       rdbdriver.StateAvailable,
		VPCSecurityGroups:           append([]string(nil), cfg.VPCSecurityGroups...),
		SubnetGroupName:             cfg.SubnetGroupName,
		DBClusterParameterGroupName: cfg.DBClusterParameterGroupName,
		NodeType:                    cfg.NodeType,
		NumberOfNodes:               numberOfNodes,
		Encrypted:                   cfg.Encrypted,
		KmsKeyID:                    resolveKMSKeyID(cfg.Encrypted, cfg.KmsKeyID),
		PubliclyAccessible:          cfg.PubliclyAccessible,
		AvailabilityZone:            cfg.AvailabilityZone,
		CreatedAt:                   m.opts.Clock.Now().UTC(),
		Tags:                        copyTags(cfg.Tags),
	}

	m.clusters.Set(cfg.ID, cluster)

	return cluster, nil
}

// finalizeCluster writes the engine's reachable endpoint back onto the stored
// cluster row under a re-acquired lock.
func (m *Mock) finalizeCluster(id, endpoint string, port int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cluster, ok := m.clusters.Get(id)
	if !ok {
		return
	}

	cluster.Endpoint = endpoint
	cluster.Port = port
	m.clusters.Set(id, cluster)
}

// rollbackCluster removes the reserved cluster when the engine provision fails.
func (m *Mock) rollbackCluster(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.clusters.Delete(id)
}

// provisionEngine backs the cluster with the wired real database engine, keyed
// and named by the cluster ID, and overrides the synthetic endpoint with the
// real host:port. It is a no-op when no engine is wired in. Redshift routes to
// the Postgres engine because it speaks the Postgres wire protocol.
//
//nolint:gocritic // cfg matches the driver interface signature.
func (m *Mock) provisionEngine(ctx context.Context, cluster *rdbdriver.Cluster, cfg rdbdriver.ClusterConfig) error {
	if m.opts.DatabaseEngine == nil {
		return nil
	}

	dbName := cfg.DatabaseName
	if dbName == "" {
		dbName = cfg.ID
	}

	shared := rdbdriver.Instance{ID: cfg.ID, Engine: cluster.Engine}
	sharedCfg := rdbdriver.InstanceConfig{
		ID:                 cfg.ID,
		Engine:             cluster.Engine,
		DBName:             dbName,
		MasterUsername:     cfg.MasterUsername,
		MasterUserPassword: cfg.MasterUserPassword,
	}

	if err := dbengine.Provision(ctx, m.opts.DatabaseEngine, &shared, &sharedCfg); err != nil {
		return err
	}

	if shared.Endpoint != "" {
		cluster.Endpoint = shared.Endpoint
		cluster.Port = shared.Port
	}

	return nil
}

// DescribeClusters returns all clusters if ids is empty, else only matching ones.
func (m *Mock) DescribeClusters(_ context.Context, ids []string) ([]rdbdriver.Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(ids) == 0 {
		all := m.clusters.All()
		out := make([]rdbdriver.Cluster, 0, len(all))

		//nolint:gocritic // map values are large structs but we need a flat slice for the API.
		for _, v := range all {
			v.State = m.settleClusterState(v.ID, v.State)
			out = append(out, v)
		}

		return out, nil
	}

	out := make([]rdbdriver.Cluster, 0, len(ids))

	for _, id := range ids {
		cluster, ok := m.clusters.Get(id)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "Redshift cluster %q not found", id)
		}

		cluster.State = m.settleClusterState(id, cluster.State)
		out = append(out, cluster)
	}

	return out, nil
}

// ModifyCluster applies changes.
func (m *Mock) ModifyCluster(
	_ context.Context, id string, input rdbdriver.ModifyInstanceInput,
) (*rdbdriver.Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cluster, ok := m.clusters.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Redshift cluster %q not found", id)
	}

	if input.EngineVersion != "" {
		cluster.EngineVersion = input.EngineVersion
	}

	applyResize(&cluster, &input)

	if input.Tags != nil {
		cluster.Tags = copyTags(input.Tags)
	}

	m.clusters.Set(id, cluster)

	// Under AsyncSettle a modified cluster briefly reports modifying before
	// settling back to available (ModifyCluster → modifying → available); a no-op
	// when settle is off.
	m.clusterSettle.Begin(id, rdbdriver.StateModifying, m.opts.Clock.Now(),
		m.opts.SettleDuration(settle.DefaultWarehouseResize))

	out := cluster
	out.State = m.settleClusterState(id, out.State)

	return &out, nil
}

// applyResize applies a Redshift ModifyCluster resize (NodeType / NumberOfNodes
// / ClusterType) onto the cluster. A "single-node" ClusterType forces one node;
// otherwise a positive NumberOfNodes is applied. Zero / empty inputs mean "no
// change", so RDS/Aurora modifications (which never set these) are unaffected.
func applyResize(cluster *rdbdriver.Cluster, input *rdbdriver.ModifyInstanceInput) {
	if input.NodeType != "" {
		cluster.NodeType = input.NodeType
	}

	switch input.ClusterType {
	case "single-node":
		cluster.NumberOfNodes = singleNodeCount
	case "multi-node":
		if input.NumberOfNodes > 0 {
			cluster.NumberOfNodes = input.NumberOfNodes
		}
	default:
		if input.NumberOfNodes > 0 {
			cluster.NumberOfNodes = input.NumberOfNodes
		}
	}
}

// DeleteCluster removes a cluster and tears down the real database backing it,
// if any. The engine Deprovision runs WITHOUT the provider lock held.
func (m *Mock) DeleteCluster(ctx context.Context, id string) error {
	// No engine wired in: remove the row under a single lock, as before.
	if m.opts.DatabaseEngine == nil {
		m.mu.Lock()
		defer m.mu.Unlock()

		if !m.clusters.Delete(id) {
			return cerrors.Newf(cerrors.NotFound, "Redshift cluster %q not found", id)
		}

		m.clusterSettle.Clear(id)

		return nil
	}

	// Capture the engine family under the lock, deprovision the real database
	// WITHOUT it, then remove the row under a re-acquired lock.
	m.mu.Lock()
	cluster, ok := m.clusters.Get(id)
	m.mu.Unlock()

	if !ok {
		return cerrors.Newf(cerrors.NotFound, "Redshift cluster %q not found", id)
	}

	inst := rdbdriver.Instance{ID: id, Engine: cluster.Engine}
	if err := dbengine.Deprovision(ctx, m.opts.DatabaseEngine, &inst); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.clusters.Delete(id)
	m.clusterSettle.Clear(id)

	return nil
}

// StartCluster moves a paused cluster to available.
func (m *Mock) StartCluster(_ context.Context, id string) error {
	return m.transitionCluster(id, rdbdriver.StateStopped, rdbdriver.StateAvailable, "start")
}

// StopCluster moves an available cluster to paused (mapped to "stopped" in the driver).
func (m *Mock) StopCluster(_ context.Context, id string) error {
	return m.transitionCluster(id, rdbdriver.StateAvailable, rdbdriver.StateStopped, "stop")
}

// RebootCluster cycles a cluster — emits running-value metrics and leaves it available.
func (m *Mock) RebootCluster(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cluster, ok := m.clusters.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "Redshift cluster %q not found", id)
	}

	if cluster.State != rdbdriver.StateAvailable {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"Redshift cluster %q is in state %q; reboot requires %q", id, cluster.State, rdbdriver.StateAvailable)
	}

	cluster.State = rdbdriver.StateAvailable
	m.clusters.Set(id, cluster)

	m.emitClusterMetrics(id, cpuUtilizationRunning, databaseConnectionsRun,
		readIOPSRunning, writeIOPSRunning, networkReceiveThroughput)

	return nil
}

// clusterStatePaused is the Redshift-only lifecycle state a paused cluster
// reports. PauseCluster/ResumeCluster move a cluster between available and
// paused; the emulator applies the transition immediately (no pausing/resuming
// intermediate).
const clusterStatePaused = "paused"

// PauseCluster suspends an available cluster (available → paused). It is part
// of the AWS-only optional clusterPauser surface, discovered by the wire
// handler via type assertion.
func (m *Mock) PauseCluster(_ context.Context, id string) (*rdbdriver.Cluster, error) {
	if err := m.transitionCluster(id, rdbdriver.StateAvailable, clusterStatePaused, "pause"); err != nil {
		return nil, err
	}

	return m.snapshotCluster(id)
}

// ResumeCluster resumes a paused cluster (paused → available).
func (m *Mock) ResumeCluster(_ context.Context, id string) (*rdbdriver.Cluster, error) {
	if err := m.transitionCluster(id, clusterStatePaused, rdbdriver.StateAvailable, "resume"); err != nil {
		return nil, err
	}

	return m.snapshotCluster(id)
}

// snapshotCluster returns a copy of the stored cluster by id.
func (m *Mock) snapshotCluster(id string) (*rdbdriver.Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cluster, ok := m.clusters.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Redshift cluster %q not found", id)
	}

	out := cluster

	return &out, nil
}

func (m *Mock) transitionCluster(id, from, to, verb string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cluster, ok := m.clusters.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "Redshift cluster %q not found", id)
	}

	if cluster.State == to {
		return nil // idempotent
	}

	if cluster.State != from {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"Redshift cluster %q is in state %q; %s requires %q", id, cluster.State, verb, from)
	}

	cluster.State = to
	m.clusters.Set(id, cluster)
	// An explicit lifecycle transition (start/stop/pause/resume) supersedes any
	// still-active create/modify settle window, so drop it.
	m.clusterSettle.Clear(id)

	return nil
}

// CreateSnapshot is unsupported — Redshift only has cluster snapshots.
func (*Mock) CreateSnapshot(_ context.Context, _ rdbdriver.SnapshotConfig) (*rdbdriver.Snapshot, error) {
	return nil, errInstanceOpsUnsupported
}

// DescribeSnapshots is unsupported in Redshift.
func (*Mock) DescribeSnapshots(_ context.Context, _ []string, _ string) ([]rdbdriver.Snapshot, error) {
	return nil, errInstanceOpsUnsupported
}

// DeleteSnapshot is unsupported in Redshift.
func (*Mock) DeleteSnapshot(_ context.Context, _ string) error {
	return errInstanceOpsUnsupported
}

// RestoreInstanceFromSnapshot is unsupported in Redshift.
func (*Mock) RestoreInstanceFromSnapshot(
	_ context.Context, _ rdbdriver.RestoreInstanceInput,
) (*rdbdriver.Instance, error) {
	return nil, errInstanceOpsUnsupported
}

// CreateClusterSnapshot snapshots a Redshift cluster.
func (m *Mock) CreateClusterSnapshot(
	_ context.Context, cfg rdbdriver.ClusterSnapshotConfig,
) (*rdbdriver.ClusterSnapshot, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "SnapshotIdentifier is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	cluster, ok := m.clusters.Get(cfg.ClusterID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Redshift cluster %q not found", cfg.ClusterID)
	}

	if _, ok := m.clusterSnapshots.Get(cfg.ID); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "Redshift cluster snapshot %q already exists", cfg.ID)
	}

	snap := rdbdriver.ClusterSnapshot{
		ID:                         cfg.ID,
		ARN:                        clusterSnapshotARN(m.opts.Region, m.opts.AccountID, cfg.ID),
		ClusterID:                  cfg.ClusterID,
		Engine:                     cluster.Engine,
		EngineVersion:              cluster.EngineVersion,
		State:                      rdbdriver.SnapshotAvailable,
		NodeType:                   cluster.NodeType,
		NumberOfNodes:              cluster.NumberOfNodes,
		Encrypted:                  cluster.Encrypted,
		KmsKeyID:                   cluster.KmsKeyID,
		TotalBackupSizeInMegaBytes: snapshotBackupSizeMB,
		MasterUsername:             cluster.MasterUsername,
		DatabaseName:               cluster.DatabaseName,
		CreatedAt:                  m.opts.Clock.Now().UTC(),
		Tags:                       copyTags(cfg.Tags),
	}

	m.clusterSnapshots.Set(cfg.ID, snap)

	out := snap

	return &out, nil
}

// DescribeClusterSnapshots returns cluster snapshots filtered by ids and/or cluster.
func (m *Mock) DescribeClusterSnapshots(
	_ context.Context, ids []string, clusterID string,
) ([]rdbdriver.ClusterSnapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all := m.clusterSnapshots.All()
	idSet := stringSet(ids)

	out := make([]rdbdriver.ClusterSnapshot, 0, len(all))

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
		return cerrors.Newf(cerrors.NotFound, "Redshift cluster snapshot %q not found", id)
	}

	return nil
}

// RestoreClusterFromSnapshot creates a new cluster from a cluster snapshot.
func (m *Mock) RestoreClusterFromSnapshot(
	_ context.Context, input rdbdriver.RestoreClusterInput,
) (*rdbdriver.Cluster, error) {
	if input.NewClusterID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "ClusterIdentifier is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	snap, ok := m.clusterSnapshots.Get(input.SnapshotID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Redshift cluster snapshot %q not found", input.SnapshotID)
	}

	if _, ok := m.clusters.Get(input.NewClusterID); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "Redshift cluster %q already exists", input.NewClusterID)
	}

	now := m.opts.Clock.Now().UTC()

	numberOfNodes := snap.NumberOfNodes
	if numberOfNodes == 0 {
		numberOfNodes = singleNodeCount
	}

	cluster := rdbdriver.Cluster{
		ID:             input.NewClusterID,
		ARN:            clusterARN(m.opts.Region, m.opts.AccountID, input.NewClusterID),
		Engine:         snap.Engine,
		EngineVersion:  snap.EngineVersion,
		MasterUsername: snap.MasterUsername,
		DatabaseName:   snap.DatabaseName,
		Endpoint:       endpointFor(input.NewClusterID),
		Port:           defaultPort,
		State:          rdbdriver.StateAvailable,
		NodeType:       snap.NodeType,
		NumberOfNodes:  numberOfNodes,
		Encrypted:      snap.Encrypted,
		KmsKeyID:       restoredKMSKeyID(snap.Encrypted, snap.KmsKeyID, input.KmsKeyID),
		CreatedAt:      now,
		Tags:           copyTags(input.Tags),
	}

	m.clusters.Set(input.NewClusterID, cluster)

	m.emitClusterMetrics(input.NewClusterID, cpuUtilizationRunning, databaseConnectionsRun,
		readIOPSRunning, writeIOPSRunning, networkReceiveThroughput)

	// Restore is a create: report creating until the window elapses (a no-op when
	// settle is off).
	m.clusterSettle.Begin(input.NewClusterID, rdbdriver.StateCreating, m.opts.Clock.Now(),
		m.opts.SettleDuration(settle.DefaultClusterSettle))

	out := cluster
	out.State = m.settleClusterState(input.NewClusterID, out.State)

	return &out, nil
}

// resolveKMSKeyID mirrors real Redshift: an encrypted cluster created without an
// explicit KmsKeyId gets the account's default key; an explicit key always wins,
// and an unencrypted cluster carries no key.
func resolveKMSKeyID(encrypted bool, kmsKeyID string) string {
	if !encrypted {
		return ""
	}

	if kmsKeyID == "" {
		return defaultKMSKeyAlias
	}

	return kmsKeyID
}

// restoredKMSKeyID picks the encryption key for a cluster restored from a
// snapshot: an unencrypted snapshot yields no key; a RestoreFromClusterSnapshot
// KmsKeyId override wins; otherwise the restored cluster inherits the snapshot's
// key (falling back to the account default if the snapshot predates key capture).
func restoredKMSKeyID(encrypted bool, snapKmsKeyID, overrideKmsKeyID string) string {
	if !encrypted {
		return ""
	}

	if overrideKmsKeyID != "" {
		return overrideKmsKeyID
	}

	if snapKmsKeyID == "" {
		return defaultKMSKeyAlias
	}

	return snapKmsKeyID
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
