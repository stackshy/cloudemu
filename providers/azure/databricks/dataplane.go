package databricks

import (
	"context"
	"strconv"

	"github.com/stackshy/cloudemu/databricks/driver"
	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
)

// --- instance pools ---

// CreateInstancePool creates an instance pool in the ACTIVE state.
func (m *Mock) CreateInstancePool(_ context.Context, cfg driver.InstancePoolConfig) (*driver.InstancePool, error) {
	switch {
	case cfg.Name == "":
		return nil, errors.New(errors.InvalidArgument, "instance_pool_name is required")
	case cfg.NodeTypeID == "":
		return nil, errors.New(errors.InvalidArgument, "node_type_id is required")
	}

	id := idgen.GenerateID("pool-")
	pool := &driver.InstancePool{
		ID:                                 id,
		Name:                               cfg.Name,
		NodeTypeID:                         cfg.NodeTypeID,
		State:                              driver.PoolActive,
		MinIdleInstances:                   cfg.MinIdleInstances,
		MaxCapacity:                        cfg.MaxCapacity,
		IdleInstanceAutoterminationMinutes: cfg.IdleInstanceAutoterminationMinutes,
		CustomTags:                         cfg.CustomTags,
	}
	m.pools.Set(id, pool)

	result := *pool

	return &result, nil
}

// GetInstancePool returns an instance pool by ID.
func (m *Mock) GetInstancePool(_ context.Context, id string) (*driver.InstancePool, error) {
	pool, ok := m.pools.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "instance pool %q not found", id)
	}

	result := *pool

	return &result, nil
}

// ListInstancePools lists all instance pools.
func (m *Mock) ListInstancePools(_ context.Context) ([]driver.InstancePool, error) {
	all := m.pools.All()
	out := make([]driver.InstancePool, 0, len(all))

	for _, p := range all {
		out = append(out, *p)
	}

	return out, nil
}

// EditInstancePool updates a pool's mutable fields.
func (m *Mock) EditInstancePool(_ context.Context, id string, cfg driver.InstancePoolConfig) error {
	pool, ok := m.pools.Get(id)
	if !ok {
		return errors.Newf(errors.NotFound, "instance pool %q not found", id)
	}

	updated := *pool
	updated.Name = cfg.Name
	updated.NodeTypeID = cfg.NodeTypeID
	updated.MinIdleInstances = cfg.MinIdleInstances
	updated.MaxCapacity = cfg.MaxCapacity
	updated.IdleInstanceAutoterminationMinutes = cfg.IdleInstanceAutoterminationMinutes
	updated.CustomTags = cfg.CustomTags
	m.pools.Set(id, &updated)

	return nil
}

// DeleteInstancePool deletes an instance pool by ID.
func (m *Mock) DeleteInstancePool(_ context.Context, id string) error {
	if !m.pools.Delete(id) {
		return errors.Newf(errors.NotFound, "instance pool %q not found", id)
	}

	return nil
}

// --- clusters ---

// CreateCluster creates a cluster in the RUNNING state.
//
//nolint:gocritic // cfg matches the driver.DataPlane interface signature (by value)
func (m *Mock) CreateCluster(_ context.Context, cfg driver.ClusterConfig) (*driver.Cluster, error) {
	switch {
	case cfg.SparkVersion == "":
		return nil, errors.New(errors.InvalidArgument, "spark_version is required")
	case cfg.NodeTypeID == "":
		return nil, errors.New(errors.InvalidArgument, "node_type_id is required")
	}

	id := idgen.GenerateID("cluster-")
	cluster := &driver.Cluster{
		ID:            id,
		Name:          cfg.Name,
		SparkVersion:  cfg.SparkVersion,
		NodeTypeID:    cfg.NodeTypeID,
		State:         driver.ClusterRunning,
		NumWorkers:    cfg.NumWorkers,
		AutoscaleMin:  cfg.AutoscaleMin,
		AutoscaleMax:  cfg.AutoscaleMax,
		RuntimeEngine: cfg.RuntimeEngine,
		CustomTags:    cfg.CustomTags,
	}
	m.clusters.Set(id, cluster)

	result := *cluster

	return &result, nil
}

// GetCluster returns a cluster by ID.
func (m *Mock) GetCluster(_ context.Context, id string) (*driver.Cluster, error) {
	cluster, ok := m.clusters.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "cluster %q not found", id)
	}

	result := *cluster

	return &result, nil
}

// ListClusters lists all clusters.
func (m *Mock) ListClusters(_ context.Context) ([]driver.Cluster, error) {
	all := m.clusters.All()
	out := make([]driver.Cluster, 0, len(all))

	for _, c := range all {
		out = append(out, *c)
	}

	return out, nil
}

// EditCluster updates a cluster's mutable fields.
//
//nolint:gocritic // cfg matches the driver.DataPlane interface signature (by value)
func (m *Mock) EditCluster(_ context.Context, id string, cfg driver.ClusterConfig) error {
	cluster, ok := m.clusters.Get(id)
	if !ok {
		return errors.Newf(errors.NotFound, "cluster %q not found", id)
	}

	updated := *cluster
	updated.Name = cfg.Name
	updated.SparkVersion = cfg.SparkVersion
	updated.NodeTypeID = cfg.NodeTypeID
	updated.NumWorkers = cfg.NumWorkers
	updated.AutoscaleMin = cfg.AutoscaleMin
	updated.AutoscaleMax = cfg.AutoscaleMax
	updated.RuntimeEngine = cfg.RuntimeEngine
	updated.CustomTags = cfg.CustomTags
	m.clusters.Set(id, &updated)

	return nil
}

// DeleteCluster terminates a cluster (it remains gettable in the TERMINATED
// state, matching real Databricks).
func (m *Mock) DeleteCluster(_ context.Context, id string) error {
	return m.setClusterState(id, driver.ClusterTerminated)
}

// StartCluster moves a terminated cluster back to RUNNING.
func (m *Mock) StartCluster(_ context.Context, id string) error {
	return m.setClusterState(id, driver.ClusterRunning)
}

// RestartCluster restarts a cluster (ends RUNNING).
func (m *Mock) RestartCluster(_ context.Context, id string) error {
	return m.setClusterState(id, driver.ClusterRunning)
}

func (m *Mock) setClusterState(id, state string) error {
	cluster, ok := m.clusters.Get(id)
	if !ok {
		return errors.Newf(errors.NotFound, "cluster %q not found", id)
	}

	updated := *cluster
	updated.State = state
	m.clusters.Set(id, &updated)

	return nil
}

// PermanentDeleteCluster removes a cluster entirely.
func (m *Mock) PermanentDeleteCluster(_ context.Context, id string) error {
	if !m.clusters.Delete(id) {
		return errors.Newf(errors.NotFound, "cluster %q not found", id)
	}

	return nil
}

// --- jobs ---

// CreateJob creates a job and returns its numeric ID.
func (m *Mock) CreateJob(_ context.Context, cfg driver.JobConfig) (int64, error) {
	if cfg.Name == "" {
		return 0, errors.New(errors.InvalidArgument, "job name is required")
	}

	id := m.jobSeq.Add(1)
	m.jobs.Set(jobKey(id), &driver.Job{
		ID:              id,
		Name:            cfg.Name,
		CreatorUserName: "emulator@cloudemu.dev",
		CreatedTime:     m.opts.Clock.Now().UTC().UnixMilli(),
		SettingsJSON:    cloneBytes(cfg.SettingsJSON),
	})

	return id, nil
}

// GetJob returns a job by ID.
func (m *Mock) GetJob(_ context.Context, id int64) (*driver.Job, error) {
	job, ok := m.jobs.Get(jobKey(id))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "job %d not found", id)
	}

	result := *job
	result.SettingsJSON = cloneBytes(job.SettingsJSON)

	return &result, nil
}

// ListJobs lists all jobs.
func (m *Mock) ListJobs(_ context.Context) ([]driver.Job, error) {
	all := m.jobs.All()
	out := make([]driver.Job, 0, len(all))

	for _, j := range all {
		job := *j
		job.SettingsJSON = cloneBytes(j.SettingsJSON)
		out = append(out, job)
	}

	return out, nil
}

// UpdateJob applies partial settings to a job.
func (m *Mock) UpdateJob(_ context.Context, id int64, cfg driver.JobConfig) error {
	return m.mutateJob(id, cfg)
}

// ResetJob overwrites a job's settings.
func (m *Mock) ResetJob(_ context.Context, id int64, cfg driver.JobConfig) error {
	return m.mutateJob(id, cfg)
}

func (m *Mock) mutateJob(id int64, cfg driver.JobConfig) error {
	job, ok := m.jobs.Get(jobKey(id))
	if !ok {
		return errors.Newf(errors.NotFound, "job %d not found", id)
	}

	updated := *job
	if cfg.Name != "" {
		updated.Name = cfg.Name
	}

	if len(cfg.SettingsJSON) > 0 {
		updated.SettingsJSON = cloneBytes(cfg.SettingsJSON)
	}

	m.jobs.Set(jobKey(id), &updated)

	return nil
}

// DeleteJob removes a job by ID.
func (m *Mock) DeleteJob(_ context.Context, id int64) error {
	if !m.jobs.Delete(jobKey(id)) {
		return errors.Newf(errors.NotFound, "job %d not found", id)
	}

	return nil
}

// RunJobNow triggers a run and returns its run ID. The run completes
// synchronously (TERMINATED / SUCCESS) so Get/List/Output are deterministic.
func (m *Mock) RunJobNow(_ context.Context, id int64) (int64, error) {
	job, ok := m.jobs.Get(jobKey(id))
	if !ok {
		return 0, errors.Newf(errors.NotFound, "job %d not found", id)
	}

	runID := m.runSeq.Add(1)
	now := m.opts.Clock.Now().UTC().UnixMilli()
	m.runs.Set(jobKey(runID), &driver.Run{
		RunID:          runID,
		JobID:          id,
		RunName:        job.Name,
		LifeCycleState: driver.RunTerminated,
		ResultState:    driver.ResultSuccess,
		StateMessage:   "Run completed",
		StartTime:      now,
		EndTime:        now,
	})

	return runID, nil
}

// --- permissions ---

// GetPermissions returns the permissions on an object, or an empty set if none
// have been assigned.
func (m *Mock) GetPermissions(_ context.Context, objectType, objectID string) (*driver.ObjectPermissions, error) {
	if p, ok := m.permissions.Get(permKey(objectType, objectID)); ok {
		return clonePermissions(p), nil
	}

	return &driver.ObjectPermissions{ObjectID: objectID, ObjectType: objectType}, nil
}

// SetPermissions replaces the access-control list on an object.
func (m *Mock) SetPermissions(
	_ context.Context, objectType, objectID string, acl []driver.AccessControl,
) (*driver.ObjectPermissions, error) {
	p := &driver.ObjectPermissions{ObjectID: objectID, ObjectType: objectType, AccessControlList: cloneACL(acl)}
	m.permissions.Set(permKey(objectType, objectID), p)

	return clonePermissions(p), nil
}

// UpdatePermissions merges entries into an object's access-control list,
// replacing any entry for the same principal.
func (m *Mock) UpdatePermissions(
	_ context.Context, objectType, objectID string, acl []driver.AccessControl,
) (*driver.ObjectPermissions, error) {
	k := permKey(objectType, objectID)

	existing, ok := m.permissions.Get(k)
	if !ok {
		return m.SetPermissions(context.Background(), objectType, objectID, acl)
	}

	merged := cloneACL(existing.AccessControlList)
	for _, entry := range acl {
		merged = upsertACL(merged, entry)
	}

	p := &driver.ObjectPermissions{ObjectID: objectID, ObjectType: objectType, AccessControlList: merged}
	m.permissions.Set(k, p)

	return clonePermissions(p), nil
}

// --- helpers ---

func jobKey(id int64) string {
	return strconv.FormatInt(id, 10)
}

func permKey(objectType, objectID string) string {
	return objectType + "/" + objectID
}

func principalOf(a driver.AccessControl) string {
	switch {
	case a.UserName != "":
		return "user:" + a.UserName
	case a.GroupName != "":
		return "group:" + a.GroupName
	default:
		return "sp:" + a.ServicePrincipalName
	}
}

func upsertACL(list []driver.AccessControl, entry driver.AccessControl) []driver.AccessControl {
	for i := range list {
		if principalOf(list[i]) == principalOf(entry) {
			list[i] = entry

			return list
		}
	}

	return append(list, entry)
}

func cloneACL(in []driver.AccessControl) []driver.AccessControl {
	if in == nil {
		return nil
	}

	out := make([]driver.AccessControl, len(in))
	copy(out, in)

	return out
}

func clonePermissions(p *driver.ObjectPermissions) *driver.ObjectPermissions {
	clone := *p
	clone.AccessControlList = cloneACL(p.AccessControlList)

	return &clone
}

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}

	out := make([]byte, len(in))
	copy(out, in)

	return out
}
