package rds

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
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
	snap.ARN = snapshotARN(m.opts.Region, m.opts.AccountID, target)
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
	snap.ARN = clusterSnapshotARN(m.opts.Region, m.opts.AccountID, target)
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
// replayed.
//
//nolint:gocritic // input matches the driver interface signature.
func (m *Mock) RestoreDBInstanceToPointInTime(
	_ context.Context, input rdsdriver.RestoreInstanceToPointInTimeInput,
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

	inst := src
	inst.ID = input.TargetInstanceID
	inst.ARN = instanceARN(m.opts.Region, m.opts.AccountID, input.TargetInstanceID)
	inst.InstanceClass = instanceClass
	inst.Endpoint = endpointFor(input.TargetInstanceID, m.opts.Region, "abcd1234")
	inst.State = rdsdriver.StateAvailable
	inst.CreatedAt = m.opts.Clock.Now().UTC()
	inst.ReadReplicaSource = ""
	inst.ReadReplicaTargets = nil
	inst.Tags = copyTags(input.Tags)

	m.instances.Set(inst.ID, inst)

	m.emitInstanceMetrics(inst.ID, inst.Engine, cpuMetricRunning, connectionsRunning)

	out := inst

	return &out, nil
}

// RestoreDBClusterToPointInTime creates a new cluster cloned from a source
// cluster's current spec (no members; caller adds instances afterward).
func (m *Mock) RestoreDBClusterToPointInTime(
	_ context.Context, input rdsdriver.RestoreClusterToPointInTimeInput,
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

	cluster := src
	cluster.ID = input.TargetClusterID
	cluster.ARN = clusterARN(m.opts.Region, m.opts.AccountID, input.TargetClusterID)
	cluster.Endpoint = endpointFor(input.TargetClusterID, m.opts.Region, "cluster")
	cluster.ReaderEndpoint = endpointFor(input.TargetClusterID, m.opts.Region, "cluster-ro")
	cluster.State = rdsdriver.StateAvailable
	cluster.Members = nil
	cluster.CreatedAt = m.opts.Clock.Now().UTC()
	cluster.Tags = copyTags(input.Tags)

	m.clusters.Set(cluster.ID, cluster)

	out := cluster

	return &out, nil
}
