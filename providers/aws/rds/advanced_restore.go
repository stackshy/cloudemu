package rds

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/regionctx"
	"github.com/stackshy/cloudemu/v2/services/relationaldb/dbengine"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

var _ rdsdriver.AdvancedRestore = (*Mock)(nil)

// CopyDBSnapshot clones an existing instance snapshot under a new identifier.
//
//nolint:dupl // structurally mirrors its sibling per-resource block by design.
func (m *Mock) CopyDBSnapshot(_ context.Context, source, target string, tags map[string]string) (*rdsdriver.Snapshot, error) {
	if target == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "TargetDBSnapshotIdentifier is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	src, ok := m.snapshots.Get(source)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "DB snapshot %q not found", source)
	}

	if m.snapshots.Has(target) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "DB snapshot %q already exists", target)
	}

	snap := src
	snap.ID = target
	snap.ARN = snapshotARN(arnRegion(src.ARN, m.opts.Region), m.opts.AccountID, target)
	snap.State = rdsdriver.SnapshotAvailable
	snap.CreatedAt = m.opts.Clock.Now().UTC()

	if tags != nil {
		snap.Tags = copyTags(tags)
	} else {
		snap.Tags = copyTags(src.Tags)
	}

	m.snapshots.Set(target, snap)

	out := snap

	return &out, nil
}

// CopyDBClusterSnapshot clones an existing cluster snapshot under a new identifier.
//
//nolint:dupl // structurally mirrors its sibling per-resource block by design.
func (m *Mock) CopyDBClusterSnapshot(_ context.Context, source, target string, tags map[string]string) (*rdsdriver.ClusterSnapshot, error) {
	if target == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "TargetDBClusterSnapshotIdentifier is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	src, ok := m.clusterSnapshots.Get(source)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "DB cluster snapshot %q not found", source)
	}

	if m.clusterSnapshots.Has(target) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "DB cluster snapshot %q already exists", target)
	}

	snap := src
	snap.ID = target
	snap.ARN = clusterSnapshotARN(arnRegion(src.ARN, m.opts.Region), m.opts.AccountID, target)
	snap.State = rdsdriver.SnapshotAvailable
	snap.CreatedAt = m.opts.Clock.Now().UTC()

	if tags != nil {
		snap.Tags = copyTags(tags)
	} else {
		snap.Tags = copyTags(src.Tags)
	}

	m.clusterSnapshots.Set(target, snap)

	out := snap

	return &out, nil
}

// RestoreDBInstanceToPointInTime creates a new instance cloned from a source
// instance's current spec. The emulator has no historical timeline, so the
// restore reflects the source as it is now; RestoreTime is accepted but not
// replayed. When an engine is wired in, the restored instance is backed by its
// OWN real database (keyed by the target id) so the reported endpoint is
// reachable, not a synthetic host that resolves to nothing.
//
//nolint:gocritic // input matches the driver interface signature.
func (m *Mock) RestoreDBInstanceToPointInTime(
	ctx context.Context, input rdsdriver.RestoreInstanceToPointInTimeInput,
) (*rdsdriver.Instance, error) {
	if input.TargetInstanceID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "TargetDBInstanceIdentifier is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	src, ok := m.instances.Get(input.SourceInstanceID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "DB instance %q not found", input.SourceInstanceID)
	}

	if m.instances.Has(input.TargetInstanceID) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "DB instance %q already exists", input.TargetInstanceID)
	}

	instanceClass := input.InstanceClass
	if instanceClass == "" {
		instanceClass = src.InstanceClass
	}

	region := regionctx.RegionOr(ctx, m.opts.Region)
	inst := src
	inst.ID = input.TargetInstanceID
	inst.ARN = instanceARN(region, m.opts.AccountID, input.TargetInstanceID)
	inst.InstanceClass = instanceClass
	inst.Endpoint = endpointFor(input.TargetInstanceID, region, "abcd1234")
	inst.State = rdsdriver.StateAvailable
	inst.CreatedAt = m.opts.Clock.Now().UTC()
	inst.ReadReplicaSource = ""
	inst.ReadReplicaTargets = nil
	inst.Tags = copyTags(input.Tags)
	// A PITR restore is a standalone instance, not a member of the source's
	// cluster; copy the security-group slice so it doesn't alias the source's.
	inst.ClusterID = ""

	inst.VPCSecurityGroups = append([]string(nil), src.VPCSecurityGroups...)

	// Provision the restored instance's OWN database (keyed by the target id, so
	// it never aliases the source) through the same engine path CreateInstance
	// uses, inheriting the source's master login so it authenticates. A no-engine
	// or non-backable family keeps the synthetic endpoint untouched.
	restoreCfg := rdsdriver.InstanceConfig{
		ID:                 input.TargetInstanceID,
		Engine:             inst.Engine,
		MasterUsername:     src.MasterUsername,
		MasterUserPassword: m.rootPasswords[input.SourceInstanceID],
	}
	if err := dbengine.Provision(ctx, m.opts.DatabaseEngine, &inst, &restoreCfg); err != nil {
		return nil, err
	}

	m.instances.Set(inst.ID, inst)
	m.rootPasswords[inst.ID] = restoreCfg.MasterUserPassword

	m.emitInstanceMetrics(inst.ID, inst.Engine, cpuMetricRunning, connectionsRunning)

	out := inst

	return &out, nil
}

// RestoreDBClusterToPointInTime creates a new cluster cloned from a source
// cluster's current spec (no members; caller adds instances afterward). When an
// engine is wired in, the restored cluster's shared database is provisioned
// (keyed by the new cluster id) so the reported endpoints — and any members
// added later — reach a real database.
func (m *Mock) RestoreDBClusterToPointInTime(
	ctx context.Context, input rdsdriver.RestoreClusterToPointInTimeInput,
) (*rdsdriver.Cluster, error) {
	if input.TargetClusterID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "DBClusterIdentifier is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	src, ok := m.clusters.Get(input.SourceClusterID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "DB cluster %q not found", input.SourceClusterID)
	}

	if m.clusters.Has(input.TargetClusterID) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "DB cluster %q already exists", input.TargetClusterID)
	}

	region := regionctx.RegionOr(ctx, m.opts.Region)
	cluster := src
	cluster.ID = input.TargetClusterID
	cluster.ARN = clusterARN(region, m.opts.AccountID, input.TargetClusterID)
	cluster.Endpoint = endpointFor(input.TargetClusterID, region, "cluster")
	cluster.ReaderEndpoint = endpointFor(input.TargetClusterID, region, "cluster-ro")
	cluster.State = rdsdriver.StateAvailable
	cluster.Members = nil
	cluster.CreatedAt = m.opts.Clock.Now().UTC()
	cluster.Tags = copyTags(input.Tags)

	// Provision the shared database keyed and named by the new cluster id (an
	// Aurora cluster's members share ONE database), inheriting the source
	// cluster's master credentials. A no-engine or non-backable family keeps the
	// synthetic endpoints untouched.
	creds := m.clusterCreds[input.SourceClusterID]
	shared := rdsdriver.Instance{ID: input.TargetClusterID, Engine: cluster.Engine}
	sharedCfg := rdsdriver.InstanceConfig{
		ID:                 input.TargetClusterID,
		Engine:             cluster.Engine,
		DBName:             input.TargetClusterID,
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

	m.clusters.Set(cluster.ID, cluster)
	m.clusterCreds[cluster.ID] = clusterCred{user: creds.user, pass: creds.pass}

	out := cluster

	return &out, nil
}
