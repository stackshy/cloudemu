package alloydb

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

var _ rdsdriver.AlloyDB = (*Mock)(nil)

// validInstanceType reports whether t is a recognized AlloyDB instance type.
func validInstanceType(t string) bool {
	switch t {
	case instanceTypePrimary, instanceTypeReadPool, instanceTypeSecondary:
		return true
	default:
		return false
	}
}

// CreateAlloyDBCluster creates a PRIMARY cluster with AlloyDB-specific config.
//
//nolint:gocritic // cfg matches the AlloyDB capability signature.
func (m *Mock) CreateAlloyDBCluster(
	_ context.Context, cfg rdsdriver.AlloyDBClusterConfig,
) (*rdsdriver.Cluster, error) {
	if err := validName("cluster", cfg.ID); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.clusters.Get(cfg.ID); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "AlloyDB cluster %q already exists", cfg.ID)
	}

	version := cfg.DatabaseVersion
	if version == "" {
		version = defaultDatabaseVersion
	}

	cluster := rdsdriver.Cluster{
		ID:              cfg.ID,
		ARN:             m.clusterName(cfg.ID),
		Engine:          "alloydb-postgresql",
		EngineVersion:   version,
		MasterUsername:  cfg.InitialUser,
		Endpoint:        m.clusterName(cfg.ID) + ".alloydb",
		Port:            defaultPort,
		State:           rdsdriver.StateAvailable,
		SubnetGroupName: cfg.Network,
		CreatedAt:       m.opts.Clock.Now().UTC(),
		Tags:            copyTags(cfg.Tags),
	}

	m.clusters.Set(cfg.ID, cluster)
	m.clusterExtra[cfg.ID] = clusterExtra{
		ClusterType:            clusterTypePrimary,
		DatabaseVersion:        version,
		Network:                cfg.Network,
		AutomatedBackupEnabled: cfg.AutomatedBackupEnabled,
		ContinuousBackup:       cfg.ContinuousBackup,
		MaintenanceDay:         cfg.MaintenanceDay,
	}

	if cfg.InitialUser != "" {
		key := childKey(cfg.ID, cfg.InitialUser)
		m.users.Set(key, rdsdriver.User{Instance: cfg.ID, Name: cfg.InitialUser, Host: "%"})
	}

	out := cloneCluster(cluster)

	return &out, nil
}

// CreateSecondaryCluster creates a cross-region SECONDARY cluster from a PRIMARY.
func (m *Mock) CreateSecondaryCluster(
	_ context.Context, cfg rdsdriver.SecondaryClusterConfig,
) (*rdsdriver.Cluster, error) {
	if err := validName("cluster", cfg.ID); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	primary, ok := m.clusters.Get(cfg.PrimaryCluster)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "AlloyDB primary cluster %q not found", cfg.PrimaryCluster)
	}

	if m.clusterExtra[cfg.PrimaryCluster].ClusterType != clusterTypePrimary {
		return nil, cerrors.Newf(cerrors.FailedPrecondition,
			"cluster %q is not a PRIMARY; cannot create a secondary from it", cfg.PrimaryCluster)
	}

	if _, ok := m.clusters.Get(cfg.ID); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "AlloyDB cluster %q already exists", cfg.ID)
	}

	cluster := rdsdriver.Cluster{
		ID:            cfg.ID,
		ARN:           m.clusterName(cfg.ID),
		Engine:        primary.Engine,
		EngineVersion: primary.EngineVersion,
		Endpoint:      m.clusterName(cfg.ID) + ".alloydb",
		Port:          defaultPort,
		State:         rdsdriver.StateAvailable,
		CreatedAt:     m.opts.Clock.Now().UTC(),
		Tags:          copyTags(cfg.Tags),
	}

	m.clusters.Set(cfg.ID, cluster)
	m.clusterExtra[cfg.ID] = clusterExtra{
		ClusterType:     clusterTypeSecondary,
		DatabaseVersion: primary.EngineVersion,
		PrimaryCluster:  cfg.PrimaryCluster,
	}

	out := cloneCluster(cluster)

	return &out, nil
}

// PromoteCluster promotes a SECONDARY cluster to PRIMARY (standalone).
func (m *Mock) PromoteCluster(_ context.Context, id string) (*rdsdriver.Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cluster, ok := m.clusters.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "AlloyDB cluster %q not found", id)
	}

	extra := m.clusterExtra[id]
	if extra.ClusterType != clusterTypeSecondary {
		return nil, cerrors.Newf(cerrors.FailedPrecondition, "cluster %q is not a SECONDARY", id)
	}

	extra.ClusterType = clusterTypePrimary
	extra.PrimaryCluster = ""
	m.clusterExtra[id] = extra

	out := cloneCluster(cluster)

	return &out, nil
}

// CreateAlloyDBInstance creates an instance of the given type under a cluster.
//
//nolint:gocritic // cfg matches the AlloyDB capability signature.
func (m *Mock) CreateAlloyDBInstance(
	_ context.Context, cfg rdsdriver.AlloyDBInstanceConfig,
) (*rdsdriver.Instance, error) {
	if err := validName("instance", cfg.ID); err != nil {
		return nil, err
	}

	if cfg.ClusterID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "clusterId is required")
	}

	instType := cfg.InstanceType
	if instType == "" {
		instType = instanceTypePrimary
	}

	if !validInstanceType(instType) {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "invalid instance type %q", instType)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	cluster, ok := m.clusters.Get(cfg.ClusterID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "AlloyDB cluster %q not found", cfg.ClusterID)
	}

	key := instanceKey(cfg.ClusterID, cfg.ID)
	if _, ok := m.instances.Get(key); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists,
			"AlloyDB instance %q already exists in cluster %q", cfg.ID, cfg.ClusterID)
	}

	cpu := cfg.CPUCount
	if cpu == 0 {
		cpu = defaultCPUCount
	}

	availability := cfg.AvailabilityType
	if availability == "" {
		availability = "REGIONAL"
	}

	inst := rdsdriver.Instance{
		ID:               cfg.ID,
		ARN:              m.instanceName(cfg.ClusterID, cfg.ID),
		Engine:           cluster.Engine,
		EngineVersion:    cluster.EngineVersion,
		Endpoint:         m.instanceName(cfg.ClusterID, cfg.ID) + ".alloydb",
		Port:             defaultPort,
		State:            rdsdriver.StateAvailable,
		ClusterID:        cfg.ClusterID,
		AvailabilityZone: m.opts.Region,
		CreatedAt:        m.opts.Clock.Now().UTC(),
		Tags:             copyTags(cfg.Tags),
	}

	m.instances.Set(key, inst)
	m.instanceExtra[key] = instanceExtra{
		InstanceType:     instType,
		CPUCount:         cpu,
		NodeCount:        cfg.NodeCount,
		AvailabilityType: availability,
		IPAddress:        "10.0.0.2",
		GceZone:          m.opts.Region,
	}

	cluster.Members = append(cluster.Members, cfg.ID)
	m.clusters.Set(cfg.ClusterID, cluster)

	m.emitInstanceMetrics(cfg.ClusterID, cfg.ID, cpuMetricRunning, connRunning)

	out := cloneInstance(inst)

	return &out, nil
}

// FailoverInstance triggers a failover of a REGIONAL primary instance. The
// instance stays available; the action just verifies it exists.
func (m *Mock) FailoverInstance(_ context.Context, clusterID, instanceID string) (*rdsdriver.Instance, error) {
	return m.instanceAction(clusterID, instanceID)
}

// RestartInstance restarts an instance (the instances.restart action).
func (m *Mock) RestartInstance(_ context.Context, clusterID, instanceID string) (*rdsdriver.Instance, error) {
	return m.instanceAction(clusterID, instanceID)
}

func (m *Mock) instanceAction(clusterID, instanceID string) (*rdsdriver.Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := instanceKey(clusterID, instanceID)

	inst, ok := m.instances.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "AlloyDB instance %q not found in cluster %q", instanceID, clusterID)
	}

	out := cloneInstance(inst)

	return &out, nil
}

// AlloyDBClusterInfo returns the AlloyDB-native attributes of a cluster.
func (m *Mock) AlloyDBClusterInfo(_ context.Context, id string) (*rdsdriver.AlloyDBClusterInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.clusters.Has(id) {
		return nil, cerrors.Newf(cerrors.NotFound, "AlloyDB cluster %q not found", id)
	}

	e := m.clusterExtra[id]

	return &rdsdriver.AlloyDBClusterInfo{
		ClusterType:            e.ClusterType,
		DatabaseVersion:        e.DatabaseVersion,
		Network:                e.Network,
		AutomatedBackupEnabled: e.AutomatedBackupEnabled,
		ContinuousBackup:       e.ContinuousBackup,
		MaintenanceDay:         e.MaintenanceDay,
		PrimaryCluster:         e.PrimaryCluster,
	}, nil
}

// AlloyDBInstanceInfo returns the AlloyDB-native attributes of an instance.
func (m *Mock) AlloyDBInstanceInfo(
	_ context.Context, clusterID, instanceID string,
) (*rdsdriver.AlloyDBInstanceInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := instanceKey(clusterID, instanceID)
	if !m.instances.Has(key) {
		return nil, cerrors.Newf(cerrors.NotFound, "AlloyDB instance %q not found in cluster %q", instanceID, clusterID)
	}

	e := m.instanceExtra[key]

	return &rdsdriver.AlloyDBInstanceInfo{
		InstanceType:     e.InstanceType,
		CPUCount:         e.CPUCount,
		NodeCount:        e.NodeCount,
		AvailabilityType: e.AvailabilityType,
		IPAddress:        e.IPAddress,
		GceZone:          e.GceZone,
	}, nil
}
