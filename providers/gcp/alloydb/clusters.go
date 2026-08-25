package alloydb

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// CreateCluster creates a new AlloyDB cluster (PRIMARY). AlloyDB-specific
// attributes not carried by ClusterConfig default sensibly and can be refined
// via the AlloyDB-native capability; the common fields map as: EngineVersion →
// databaseVersion, SubnetGroupName → network, MasterUsername/Password → initial
// user, DatabaseName → initial database.
//
//nolint:gocritic // cfg matches the driver interface signature.
func (m *Mock) CreateCluster(_ context.Context, cfg rdsdriver.ClusterConfig) (*rdsdriver.Cluster, error) {
	if err := validName("cluster", cfg.ID); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.clusters.Get(cfg.ID); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "AlloyDB cluster %q already exists", cfg.ID)
	}

	version := cfg.EngineVersion
	if version == "" {
		version = defaultDatabaseVersion
	}

	port := cfg.Port
	if port == 0 {
		port = defaultPort
	}

	cluster := rdsdriver.Cluster{
		ID:                          cfg.ID,
		ARN:                         m.clusterName(cfg.ID),
		Engine:                      "alloydb-postgresql",
		EngineVersion:               version,
		MasterUsername:              cfg.MasterUsername,
		DatabaseName:                cfg.DatabaseName,
		Endpoint:                    m.clusterName(cfg.ID) + ".alloydb",
		Port:                        port,
		State:                       rdsdriver.StateAvailable,
		VPCSecurityGroups:           cloneStrings(cfg.VPCSecurityGroups),
		SubnetGroupName:             cfg.SubnetGroupName,
		DBClusterParameterGroupName: cfg.DBClusterParameterGroupName,
		CreatedAt:                   m.opts.Clock.Now().UTC(),
		Tags:                        copyTags(cfg.Tags),
	}

	m.clusters.Set(cfg.ID, cluster)
	m.clusterExtra[cfg.ID] = clusterExtra{
		ClusterType:            clusterTypePrimary,
		DatabaseVersion:        version,
		Network:                cfg.SubnetGroupName,
		AutomatedBackupEnabled: true,
		ContinuousBackup:       true,
	}
	m.initialPasswords[cfg.ID] = cfg.MasterUserPassword

	out := cloneCluster(cluster)

	return &out, nil
}

// DescribeClusters returns all clusters when ids is empty, else the named ones.
func (m *Mock) DescribeClusters(_ context.Context, ids []string) ([]rdsdriver.Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(ids) == 0 {
		all := m.clusters.SortedValues()
		out := make([]rdsdriver.Cluster, 0, len(all))

		//nolint:gocritic // map values are large structs; copy is unavoidable when materializing.
		for _, v := range all {
			out = append(out, cloneCluster(v))
		}

		return out, nil
	}

	out := make([]rdsdriver.Cluster, 0, len(ids))

	for _, id := range ids {
		c, ok := m.clusters.Get(id)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "AlloyDB cluster %q not found", id)
		}

		out = append(out, cloneCluster(c))
	}

	return out, nil
}

// ModifyCluster applies the non-zero fields of input to an existing cluster.
//
//nolint:gocritic // input matches the driver interface signature.
func (m *Mock) ModifyCluster(
	_ context.Context, id string, input rdsdriver.ModifyInstanceInput,
) (*rdsdriver.Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.clusters.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "AlloyDB cluster %q not found", id)
	}

	if input.EngineVersion != "" {
		c.EngineVersion = input.EngineVersion

		extra := m.clusterExtra[id]
		extra.DatabaseVersion = input.EngineVersion
		m.clusterExtra[id] = extra
	}

	if input.DBClusterParameterGroupName != "" {
		c.DBClusterParameterGroupName = input.DBClusterParameterGroupName
	}

	if input.Tags != nil {
		c.Tags = copyTags(input.Tags)
	}

	extra := m.clusterExtra[id]
	extra.UpdatedAt = m.opts.Clock.Now().UTC()
	m.clusterExtra[id] = extra

	m.clusters.Set(id, c)

	out := cloneCluster(c)

	return &out, nil
}

// DeleteCluster removes a cluster and cascades to its instances, users and
// databases (matching AlloyDB's force-delete of a cluster's contents).
func (m *Mock) DeleteCluster(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.clusters.Get(id); !ok {
		return cerrors.Newf(cerrors.NotFound, "AlloyDB cluster %q not found", id)
	}

	if err := m.deleteClusterChildren(ctx, id); err != nil {
		return err
	}

	m.clusters.Delete(id)
	delete(m.clusterExtra, id)
	delete(m.initialPasswords, id)
	m.detachSecondaries(id)

	return nil
}

// detachSecondaries clears the primary link on any SECONDARY cluster that
// pointed at the just-deleted primary, so a live secondary never advertises a
// ghost primary (the replica-linkage class from #303). The caller holds the
// write lock.
func (m *Mock) detachSecondaries(primaryID string) {
	for cid := range m.clusterExtra {
		extra := m.clusterExtra[cid]
		if extra.PrimaryCluster != primaryID {
			continue
		}

		extra.PrimaryCluster = ""
		m.clusterExtra[cid] = extra
	}
}

// deleteClusterChildren removes instances, users and databases under a cluster,
// tearing down each instance's real database first. The caller holds the write
// lock. The trailing '/' prefix guard prevents "foo" from sweeping "foo2/…".
func (m *Mock) deleteClusterChildren(ctx context.Context, cluster string) error {
	prefix := cluster + "/"

	for _, key := range m.instances.Keys() {
		if hasPrefix(key, prefix) {
			inst, _ := m.instances.Get(key)
			if err := m.deprovisionInstanceEngine(ctx, key, inst.EngineVersion); err != nil {
				return err
			}

			m.instances.Delete(key)
			delete(m.instanceExtra, key)
		}
	}

	for _, key := range m.users.Keys() {
		if hasPrefix(key, prefix) {
			m.users.Delete(key)
		}
	}

	for _, key := range m.databases.Keys() {
		if hasPrefix(key, prefix) {
			m.databases.Delete(key)
		}
	}

	return nil
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// StartCluster is unsupported — AlloyDB clusters cannot be stopped or started.
func (*Mock) StartCluster(_ context.Context, _ string) error {
	return cerrors.New(cerrors.InvalidArgument, "AlloyDB does not support starting clusters")
}

// StopCluster is unsupported — AlloyDB clusters cannot be stopped or started.
func (*Mock) StopCluster(_ context.Context, _ string) error {
	return cerrors.New(cerrors.InvalidArgument, "AlloyDB does not support stopping clusters")
}

// CreateClusterSnapshot creates an on-demand backup of a cluster.
func (m *Mock) CreateClusterSnapshot(
	_ context.Context, cfg rdsdriver.ClusterSnapshotConfig,
) (*rdsdriver.ClusterSnapshot, error) {
	if err := validName("backup", cfg.ID); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	cluster, ok := m.clusters.Get(cfg.ClusterID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "AlloyDB cluster %q not found", cfg.ClusterID)
	}

	if _, ok := m.backups.Get(cfg.ID); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "AlloyDB backup %q already exists", cfg.ID)
	}

	snap := rdsdriver.ClusterSnapshot{
		ID:            cfg.ID,
		ARN:           m.backupName(cfg.ID),
		ClusterID:     cfg.ClusterID,
		Engine:        cluster.Engine,
		EngineVersion: cluster.EngineVersion,
		State:         rdsdriver.SnapshotAvailable,
		CreatedAt:     m.opts.Clock.Now().UTC(),
		Tags:          copyTags(cfg.Tags),
	}

	m.backups.Set(cfg.ID, snap)
	m.backupExtra[cfg.ID] = backupExtra{Type: "ON_DEMAND"}

	out := cloneClusterSnapshot(snap)

	return &out, nil
}

// DescribeClusterSnapshots returns backups, optionally filtered by id and/or
// source cluster.
func (m *Mock) DescribeClusterSnapshots(
	_ context.Context, ids []string, clusterID string,
) ([]rdsdriver.ClusterSnapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	idSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		idSet[id] = struct{}{}
	}

	out := []rdsdriver.ClusterSnapshot{}

	snaps := m.backups.SortedValues()
	for i := range snaps {
		if clusterID != "" && snaps[i].ClusterID != clusterID {
			continue
		}

		if len(idSet) > 0 {
			if _, ok := idSet[snaps[i].ID]; !ok {
				continue
			}
		}

		out = append(out, cloneClusterSnapshot(snaps[i]))
	}

	return out, nil
}

// DeleteClusterSnapshot removes a backup.
func (m *Mock) DeleteClusterSnapshot(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.backups.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "AlloyDB backup %q not found", id)
	}

	delete(m.backupExtra, id)

	return nil
}

// RestoreClusterFromSnapshot creates a new cluster from a backup.
func (m *Mock) RestoreClusterFromSnapshot(
	_ context.Context, input rdsdriver.RestoreClusterInput,
) (*rdsdriver.Cluster, error) {
	if err := validName("cluster", input.NewClusterID); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	snap, ok := m.backups.Get(input.SnapshotID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "AlloyDB backup %q not found", input.SnapshotID)
	}

	if _, ok := m.clusters.Get(input.NewClusterID); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "AlloyDB cluster %q already exists", input.NewClusterID)
	}

	cluster := rdsdriver.Cluster{
		ID:            input.NewClusterID,
		ARN:           m.clusterName(input.NewClusterID),
		Engine:        snap.Engine,
		EngineVersion: snap.EngineVersion,
		Endpoint:      m.clusterName(input.NewClusterID) + ".alloydb",
		Port:          defaultPort,
		State:         rdsdriver.StateAvailable,
		CreatedAt:     m.opts.Clock.Now().UTC(),
		Tags:          copyTags(input.Tags),
	}

	m.clusters.Set(input.NewClusterID, cluster)
	m.clusterExtra[input.NewClusterID] = clusterExtra{
		ClusterType:            clusterTypePrimary,
		DatabaseVersion:        snap.EngineVersion,
		AutomatedBackupEnabled: true,
		ContinuousBackup:       true,
	}

	out := cloneCluster(cluster)

	return &out, nil
}
