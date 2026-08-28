package rds

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/regionctx"
	dbengine "github.com/stackshy/cloudemu/v2/services/relationaldb/dbengine"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

var _ rdsdriver.ReadReplicas = (*Mock)(nil)

// sourceEngineBacked reports whether an instance of the given engine family is
// backed by the wired real engine, so its stored endpoint is a reachable
// host:port rather than a synthetic one.
func (m *Mock) sourceEngineBacked(engine string) bool {
	return m.opts.DatabaseEngine != nil &&
		(dbengine.IsPostgresFamily(engine) || dbengine.IsMySQLFamily(engine))
}

// CreateDBInstanceReadReplica creates a replica reading from an existing
// primary instance, inheriting the source's engine, version, storage,
// DBName, and DBParameterGroup unless the caller overrides them.
//
//nolint:gocritic // cfg matches the driver interface signature.
func (m *Mock) CreateDBInstanceReadReplica(ctx context.Context, cfg rdsdriver.ReadReplicaConfig) (*rdsdriver.Instance, error) {
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

	// Same-Region read replicas inherit the source's DBParameterGroup and
	// DBName unless the caller overrides the parameter group explicitly.
	// DBName has no override parameter on CreateDBInstanceReadReplica at all.
	dbParameterGroupName := cfg.DBParameterGroupName
	if dbParameterGroupName == "" {
		dbParameterGroupName = src.DBParameterGroupName
	}

	// A real read replica serves the SOURCE's data. When the source is backed by
	// a real engine, point the replica at the source's reachable host:port so a
	// client reading from the replica reaches the real database — provisioning a
	// separate empty database would leave the replica seeing none of the source's
	// data. A synthetic source keeps the synthetic replica endpoint.
	region := regionctx.RegionOr(ctx, m.opts.Region)

	endpoint := endpointFor(cfg.ID, region, "abcd1234")
	if m.sourceEngineBacked(src.Engine) {
		endpoint = src.Endpoint
		port = src.Port
	}

	replica := rdsdriver.Instance{
		ID:                   cfg.ID,
		ARN:                  instanceARN(region, m.opts.AccountID, cfg.ID),
		Engine:               src.Engine,
		EngineVersion:        src.EngineVersion,
		InstanceClass:        instanceClass,
		AllocatedStorage:     src.AllocatedStorage,
		StorageType:          src.StorageType,
		MasterUsername:       src.MasterUsername,
		DBName:               src.DBName,
		Endpoint:             endpoint,
		Port:                 port,
		State:                rdsdriver.StateAvailable,
		PubliclyAccessible:   cfg.PubliclyAccessible,
		AvailabilityZone:     cfg.AvailabilityZone,
		DBParameterGroupName: dbParameterGroupName,
		CreatedAt:            m.opts.Clock.Now(),
		Tags:                 copyTags(cfg.Tags),
		ReadReplicaSource:    cfg.SourceInstanceID,
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
