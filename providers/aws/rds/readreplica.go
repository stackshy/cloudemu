package rds

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

var _ rdsdriver.ReadReplicas = (*Mock)(nil)

// CreateDBInstanceReadReplica creates a replica reading from an existing
// primary instance, inheriting the source's engine, version and storage.
func (m *Mock) CreateDBInstanceReadReplica(_ context.Context, cfg rdsdriver.ReadReplicaConfig) (*rdsdriver.Instance, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "DBInstanceIdentifier is required")
	}

	if cfg.SourceInstanceID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "SourceDBInstanceIdentifier is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.instances.Has(cfg.ID) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "DB instance %q already exists", cfg.ID)
	}

	src, ok := m.instances.Get(cfg.SourceInstanceID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "DB instance %q not found", cfg.SourceInstanceID)
	}

	instanceClass := cfg.InstanceClass
	if instanceClass == "" {
		instanceClass = src.InstanceClass
	}

	port := cfg.Port
	if port == 0 {
		port = src.Port
	}

	replica := rdsdriver.Instance{
		ID:                 cfg.ID,
		ARN:                instanceARN(m.opts.Region, m.opts.AccountID, cfg.ID),
		Engine:             src.Engine,
		EngineVersion:      src.EngineVersion,
		InstanceClass:      instanceClass,
		AllocatedStorage:   src.AllocatedStorage,
		StorageType:        src.StorageType,
		MasterUsername:     src.MasterUsername,
		Endpoint:           endpointFor(cfg.ID, m.opts.Region, "abcd1234"),
		Port:               port,
		State:              rdsdriver.StateAvailable,
		PubliclyAccessible: cfg.PubliclyAccessible,
		AvailabilityZone:   cfg.AvailabilityZone,
		CreatedAt:          m.opts.Clock.Now(),
		Tags:               copyTags(cfg.Tags),
		ReadReplicaSource:  cfg.SourceInstanceID,
	}
	m.instances.Set(cfg.ID, replica)

	src.ReadReplicaTargets = append(src.ReadReplicaTargets, cfg.ID)
	m.instances.Set(src.ID, src)

	m.emitInstanceMetrics(replica.ID, replica.Engine, cpuMetricRunning, connectionsRunning)

	out := replica

	return &out, nil
}

// PromoteReadReplica detaches a replica from its primary, making it a
// standalone instance.
func (m *Mock) PromoteReadReplica(_ context.Context, id string) (*rdsdriver.Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	replica, ok := m.instances.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "DB instance %q not found", id)
	}

	if replica.ReadReplicaSource == "" {
		return nil, cerrors.Newf(cerrors.FailedPrecondition, "DB instance %q is not a read replica", id)
	}

	if src, ok := m.instances.Get(replica.ReadReplicaSource); ok {
		src.ReadReplicaTargets = removeString(src.ReadReplicaTargets, id)
		m.instances.Set(src.ID, src)
	}

	replica.ReadReplicaSource = ""
	m.instances.Set(id, replica)

	out := replica

	return &out, nil
}
