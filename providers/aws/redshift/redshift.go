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
	"sync"

	"github.com/stackshy/cloudemu/config"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	rdbdriver "github.com/stackshy/cloudemu/relationaldb/driver"
)

const (
	defaultEngine            = "redshift"
	defaultPort              = 5439
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

// Mock is the in-memory AWS Redshift implementation.
type Mock struct {
	mu sync.RWMutex

	clusters         *memstore.Store[rdbdriver.Cluster]
	clusterSnapshots *memstore.Store[rdbdriver.ClusterSnapshot]

	opts       *config.Options
	monitoring mondriver.Monitoring
}

// New creates a new AWS Redshift mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		clusters:         memstore.New[rdbdriver.Cluster](),
		clusterSnapshots: memstore.New[rdbdriver.ClusterSnapshot](),
		opts:             opts,
	}
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

// CreateCluster creates a new Redshift cluster.
//
//nolint:gocritic // cfg matches the driver interface signature.
func (m *Mock) CreateCluster(_ context.Context, cfg rdbdriver.ClusterConfig) (*rdbdriver.Cluster, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "ClusterIdentifier is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.clusters.Get(cfg.ID); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "Redshift cluster %q already exists", cfg.ID)
	}

	engine := cfg.Engine
	if engine == "" {
		engine = defaultEngine
	}

	port := cfg.Port
	if port == 0 {
		port = defaultPort
	}

	cluster := rdbdriver.Cluster{
		ID:                cfg.ID,
		ARN:               clusterARN(m.opts.Region, m.opts.AccountID, cfg.ID),
		Engine:            engine,
		EngineVersion:     cfg.EngineVersion,
		MasterUsername:    cfg.MasterUsername,
		DatabaseName:      cfg.DatabaseName,
		Endpoint:          endpointFor(cfg.ID),
		Port:              port,
		State:             rdbdriver.StateAvailable,
		VPCSecurityGroups: append([]string(nil), cfg.VPCSecurityGroups...),
		SubnetGroupName:   cfg.SubnetGroupName,
		CreatedAt:         m.opts.Clock.Now().UTC(),
		Tags:              copyTags(cfg.Tags),
	}

	m.clusters.Set(cfg.ID, cluster)

	m.emitClusterMetrics(cfg.ID, cpuUtilizationRunning, databaseConnectionsRun,
		readIOPSRunning, writeIOPSRunning, networkReceiveThroughput)

	out := cluster

	return &out, nil
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

	if input.Tags != nil {
		cluster.Tags = copyTags(input.Tags)
	}

	m.clusters.Set(id, cluster)

	out := cluster

	return &out, nil
}

// DeleteCluster removes a cluster.
func (m *Mock) DeleteCluster(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.clusters.Get(id); !ok {
		return cerrors.Newf(cerrors.NotFound, "Redshift cluster %q not found", id)
	}

	m.clusters.Delete(id)

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
		ID:            cfg.ID,
		ARN:           clusterSnapshotARN(m.opts.Region, m.opts.AccountID, cfg.ID),
		ClusterID:     cfg.ClusterID,
		Engine:        cluster.Engine,
		EngineVersion: cluster.EngineVersion,
		State:         rdbdriver.SnapshotAvailable,
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

	cluster := rdbdriver.Cluster{
		ID:            input.NewClusterID,
		ARN:           clusterARN(m.opts.Region, m.opts.AccountID, input.NewClusterID),
		Engine:        snap.Engine,
		EngineVersion: snap.EngineVersion,
		Endpoint:      endpointFor(input.NewClusterID),
		Port:          defaultPort,
		State:         rdbdriver.StateAvailable,
		CreatedAt:     now,
		Tags:          copyTags(input.Tags),
	}

	m.clusters.Set(input.NewClusterID, cluster)

	m.emitClusterMetrics(input.NewClusterID, cpuUtilizationRunning, databaseConnectionsRun,
		readIOPSRunning, writeIOPSRunning, networkReceiveThroughput)

	out := cluster

	return &out, nil
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
