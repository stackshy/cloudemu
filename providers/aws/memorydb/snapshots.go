package memorydb

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
)

func cloneSnapshot(in *mdbdriver.Snapshot) mdbdriver.Snapshot {
	s := *in
	s.Tags = copyTags(s.Tags)

	return s
}

// snapshotFromCluster captures a cluster's shape into a snapshot. The caller
// holds the write lock.
func (m *Mock) snapshotFromCluster(name string, c *mdbdriver.Cluster, source string, tags map[string]string) mdbdriver.Snapshot {
	return mdbdriver.Snapshot{
		Name:        name,
		ARN:         m.arn("snapshot", name),
		Status:      mdbdriver.StatusAvailable,
		Source:      source,
		DataTiering: c.DataTiering,
		ClusterConfiguration: mdbdriver.ClusterConfiguration{
			Name:                   c.Name,
			NodeType:               c.NodeType,
			Engine:                 c.Engine,
			EngineVersion:          c.EngineVersion,
			ParameterGroupName:     c.ParameterGroupName,
			SubnetGroupName:        c.SubnetGroupName,
			MaintenanceWindow:      c.MaintenanceWindow,
			SnapshotWindow:         c.SnapshotWindow,
			TopicARN:               c.SnsTopicARN,
			NumShards:              c.NumberOfShards,
			Port:                   c.ClusterEndpoint.Port,
			SnapshotRetentionLimit: c.SnapshotRetentionLimit,
		},
		Tags:      copyTags(tags),
		CreatedAt: m.opts.Clock.Now().UTC(),
	}
}

// CreateSnapshot takes a manual snapshot of a cluster.
func (m *Mock) CreateSnapshot(_ context.Context, cfg mdbdriver.CreateSnapshotConfig) (*mdbdriver.Snapshot, error) {
	if err := validName("snapshot", cfg.Name); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.clusters.Get(cfg.ClusterName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", cfg.ClusterName)
	}

	if m.snapshots.Has(cfg.Name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "snapshot %q already exists", cfg.Name)
	}

	snap := m.snapshotFromCluster(cfg.Name, &c, "manual", cfg.Tags)
	snap.KmsKeyID = cfg.KmsKeyID
	m.snapshots.Set(cfg.Name, snap)

	out := cloneSnapshot(&snap)

	return &out, nil
}

// DescribeSnapshots returns snapshots, optionally filtered by name and/or
// source cluster.
func (m *Mock) DescribeSnapshots(_ context.Context, names []string, clusterName string) ([]mdbdriver.Snapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	nameSet := make(map[string]struct{}, len(names))
	for _, n := range names {
		nameSet[n] = struct{}{}
	}

	out := []mdbdriver.Snapshot{}

	all := m.snapshots.SortedValues()
	for i := range all {
		if clusterName != "" && all[i].ClusterConfiguration.Name != clusterName {
			continue
		}

		if len(nameSet) > 0 {
			if _, ok := nameSet[all[i].Name]; !ok {
				continue
			}
		}

		out = append(out, cloneSnapshot(&all[i]))
	}

	return out, nil
}

// CopySnapshot copies an existing snapshot to a new name.
func (m *Mock) CopySnapshot(_ context.Context, cfg mdbdriver.CopySnapshotConfig) (*mdbdriver.Snapshot, error) {
	if err := validName("snapshot", cfg.TargetName); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	src, ok := m.snapshots.Get(cfg.SourceName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "snapshot %q not found", cfg.SourceName)
	}

	if m.snapshots.Has(cfg.TargetName) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "snapshot %q already exists", cfg.TargetName)
	}

	copySnap := src
	copySnap.Name = cfg.TargetName
	copySnap.ARN = m.arn("snapshot", cfg.TargetName)
	copySnap.Source = "copied"
	copySnap.KmsKeyID = cfg.KmsKeyID
	copySnap.Tags = copyTags(cfg.Tags)
	copySnap.CreatedAt = m.opts.Clock.Now().UTC()
	m.snapshots.Set(cfg.TargetName, copySnap)

	out := cloneSnapshot(&copySnap)

	return &out, nil
}

// DeleteSnapshot removes a snapshot.
func (m *Mock) DeleteSnapshot(_ context.Context, name string) (*mdbdriver.Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	snap, ok := m.snapshots.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "snapshot %q not found", name)
	}

	m.snapshots.Delete(name)

	snap.Status = mdbdriver.StatusDeleting

	out := cloneSnapshot(&snap)

	return &out, nil
}
