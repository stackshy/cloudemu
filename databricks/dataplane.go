package databricks

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/databricks/driver"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
)

// DataPlane is the portable wrapper for the Databricks workspace data plane
// (instance pools, clusters, jobs, permissions) with cross-cutting concerns.
type DataPlane struct {
	driver   driver.DataPlane
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

// NewDataPlane creates a portable DataPlane wrapping the given driver.
func NewDataPlane(d driver.DataPlane, opts ...DataPlaneOption) *DataPlane {
	dp := &DataPlane{driver: d}
	for _, opt := range opts {
		opt(dp)
	}

	return dp
}

// DataPlaneOption configures a portable DataPlane.
type DataPlaneOption func(*DataPlane)

// WithDataPlaneRecorder sets the recorder.
func WithDataPlaneRecorder(r *recorder.Recorder) DataPlaneOption {
	return func(d *DataPlane) { d.recorder = r }
}

// WithDataPlaneMetrics sets the metrics collector.
func WithDataPlaneMetrics(m *metrics.Collector) DataPlaneOption {
	return func(d *DataPlane) { d.metrics = m }
}

// WithDataPlaneRateLimiter sets the rate limiter.
func WithDataPlaneRateLimiter(l *ratelimit.Limiter) DataPlaneOption {
	return func(d *DataPlane) { d.limiter = l }
}

// WithDataPlaneErrorInjection sets the error injector.
func WithDataPlaneErrorInjection(i *inject.Injector) DataPlaneOption {
	return func(d *DataPlane) { d.injector = i }
}

// WithDataPlaneLatency sets simulated latency.
func WithDataPlaneLatency(dur time.Duration) DataPlaneOption {
	return func(d *DataPlane) { d.latency = dur }
}

//nolint:dupl // standard cross-cutting pipeline; intentionally mirrors Databricks.do in this package.
func (d *DataPlane) do(_ context.Context, op string, input any, fn func() (any, error)) (any, error) {
	start := time.Now()

	if d.injector != nil {
		if err := d.injector.Check("databricks-dataplane", op); err != nil {
			d.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if d.limiter != nil {
		if err := d.limiter.Allow(); err != nil {
			d.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if d.latency > 0 {
		time.Sleep(d.latency)
	}

	out, err := fn()
	dur := time.Since(start)

	if d.metrics != nil {
		labels := map[string]string{"service": "databricks-dataplane", "operation": op}
		d.metrics.Counter("calls_total", 1, labels)
		d.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			d.metrics.Counter("errors_total", 1, labels)
		}
	}

	d.rec(op, input, out, err, dur)

	return out, err
}

func (d *DataPlane) rec(op string, input, output any, err error, dur time.Duration) {
	if d.recorder != nil {
		d.recorder.Record("databricks-dataplane", op, input, output, err, dur)
	}
}

// CreateInstancePool creates an instance pool.
func (d *DataPlane) CreateInstancePool(ctx context.Context, cfg driver.InstancePoolConfig) (*driver.InstancePool, error) {
	out, err := d.do(ctx, "CreateInstancePool", cfg, func() (any, error) { return d.driver.CreateInstancePool(ctx, cfg) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.InstancePool), nil
}

// GetInstancePool retrieves an instance pool by ID.
func (d *DataPlane) GetInstancePool(ctx context.Context, id string) (*driver.InstancePool, error) {
	out, err := d.do(ctx, "GetInstancePool", id, func() (any, error) { return d.driver.GetInstancePool(ctx, id) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.InstancePool), nil
}

// ListInstancePools lists all instance pools.
func (d *DataPlane) ListInstancePools(ctx context.Context) ([]driver.InstancePool, error) {
	out, err := d.do(ctx, "ListInstancePools", nil, func() (any, error) { return d.driver.ListInstancePools(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.InstancePool), nil
}

// EditInstancePool updates an instance pool.
func (d *DataPlane) EditInstancePool(ctx context.Context, id string, cfg driver.InstancePoolConfig) error {
	_, err := d.do(ctx, "EditInstancePool", id, func() (any, error) { return nil, d.driver.EditInstancePool(ctx, id, cfg) })

	return err
}

// DeleteInstancePool deletes an instance pool by ID.
func (d *DataPlane) DeleteInstancePool(ctx context.Context, id string) error {
	_, err := d.do(ctx, "DeleteInstancePool", id, func() (any, error) { return nil, d.driver.DeleteInstancePool(ctx, id) })

	return err
}

// CreateCluster creates a cluster.
//
//nolint:gocritic // cfg matches the driver.DataPlane interface signature (by value)
func (d *DataPlane) CreateCluster(ctx context.Context, cfg driver.ClusterConfig) (*driver.Cluster, error) {
	out, err := d.do(ctx, "CreateCluster", cfg, func() (any, error) { return d.driver.CreateCluster(ctx, cfg) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.Cluster), nil
}

// GetCluster retrieves a cluster by ID.
func (d *DataPlane) GetCluster(ctx context.Context, id string) (*driver.Cluster, error) {
	out, err := d.do(ctx, "GetCluster", id, func() (any, error) { return d.driver.GetCluster(ctx, id) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.Cluster), nil
}

// ListClusters lists all clusters.
func (d *DataPlane) ListClusters(ctx context.Context) ([]driver.Cluster, error) {
	out, err := d.do(ctx, "ListClusters", nil, func() (any, error) { return d.driver.ListClusters(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.Cluster), nil
}

// EditCluster updates a cluster.
//
//nolint:gocritic // cfg matches the driver.DataPlane interface signature (by value)
func (d *DataPlane) EditCluster(ctx context.Context, id string, cfg driver.ClusterConfig) error {
	_, err := d.do(ctx, "EditCluster", id, func() (any, error) { return nil, d.driver.EditCluster(ctx, id, cfg) })

	return err
}

// DeleteCluster terminates a cluster.
func (d *DataPlane) DeleteCluster(ctx context.Context, id string) error {
	_, err := d.do(ctx, "DeleteCluster", id, func() (any, error) { return nil, d.driver.DeleteCluster(ctx, id) })

	return err
}

// PermanentDeleteCluster permanently removes a cluster.
func (d *DataPlane) PermanentDeleteCluster(ctx context.Context, id string) error {
	_, err := d.do(ctx, "PermanentDeleteCluster", id, func() (any, error) {
		return nil, d.driver.PermanentDeleteCluster(ctx, id)
	})

	return err
}

// StartCluster starts a cluster.
func (d *DataPlane) StartCluster(ctx context.Context, id string) error {
	_, err := d.do(ctx, "StartCluster", id, func() (any, error) { return nil, d.driver.StartCluster(ctx, id) })

	return err
}

// RestartCluster restarts a cluster.
func (d *DataPlane) RestartCluster(ctx context.Context, id string) error {
	_, err := d.do(ctx, "RestartCluster", id, func() (any, error) { return nil, d.driver.RestartCluster(ctx, id) })

	return err
}

// CreateJob creates a job and returns its ID.
func (d *DataPlane) CreateJob(ctx context.Context, cfg driver.JobConfig) (int64, error) {
	out, err := d.do(ctx, "CreateJob", cfg.Name, func() (any, error) { return d.driver.CreateJob(ctx, cfg) })
	if err != nil {
		return 0, err
	}

	return out.(int64), nil
}

// GetJob retrieves a job by ID.
func (d *DataPlane) GetJob(ctx context.Context, id int64) (*driver.Job, error) {
	out, err := d.do(ctx, "GetJob", id, func() (any, error) { return d.driver.GetJob(ctx, id) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.Job), nil
}

// ListJobs lists all jobs.
func (d *DataPlane) ListJobs(ctx context.Context) ([]driver.Job, error) {
	out, err := d.do(ctx, "ListJobs", nil, func() (any, error) { return d.driver.ListJobs(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.Job), nil
}

// UpdateJob applies partial settings to a job.
func (d *DataPlane) UpdateJob(ctx context.Context, id int64, cfg driver.JobConfig) error {
	_, err := d.do(ctx, "UpdateJob", id, func() (any, error) { return nil, d.driver.UpdateJob(ctx, id, cfg) })

	return err
}

// ResetJob overwrites a job's settings.
func (d *DataPlane) ResetJob(ctx context.Context, id int64, cfg driver.JobConfig) error {
	_, err := d.do(ctx, "ResetJob", id, func() (any, error) { return nil, d.driver.ResetJob(ctx, id, cfg) })

	return err
}

// DeleteJob deletes a job by ID.
func (d *DataPlane) DeleteJob(ctx context.Context, id int64) error {
	_, err := d.do(ctx, "DeleteJob", id, func() (any, error) { return nil, d.driver.DeleteJob(ctx, id) })

	return err
}

// RunJobNow triggers a job run and returns its run ID.
func (d *DataPlane) RunJobNow(ctx context.Context, id int64) (int64, error) {
	out, err := d.do(ctx, "RunJobNow", id, func() (any, error) { return d.driver.RunJobNow(ctx, id) })
	if err != nil {
		return 0, err
	}

	return out.(int64), nil
}

// GetPermissions returns the permissions on an object.
func (d *DataPlane) GetPermissions(ctx context.Context, objectType, objectID string) (*driver.ObjectPermissions, error) {
	out, err := d.do(ctx, "GetPermissions", objectType+"/"+objectID, func() (any, error) {
		return d.driver.GetPermissions(ctx, objectType, objectID)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.ObjectPermissions), nil
}

// SetPermissions replaces the access-control list on an object.
func (d *DataPlane) SetPermissions(
	ctx context.Context, objectType, objectID string, acl []driver.AccessControl,
) (*driver.ObjectPermissions, error) {
	out, err := d.do(ctx, "SetPermissions", objectType+"/"+objectID, func() (any, error) {
		return d.driver.SetPermissions(ctx, objectType, objectID, acl)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.ObjectPermissions), nil
}

// UpdatePermissions merges entries into an object's access-control list.
func (d *DataPlane) UpdatePermissions(
	ctx context.Context, objectType, objectID string, acl []driver.AccessControl,
) (*driver.ObjectPermissions, error) {
	out, err := d.do(ctx, "UpdatePermissions", objectType+"/"+objectID, func() (any, error) {
		return d.driver.UpdatePermissions(ctx, objectType, objectID, acl)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.ObjectPermissions), nil
}

// GetRun retrieves a job run by ID.
func (d *DataPlane) GetRun(ctx context.Context, runID int64) (*driver.Run, error) {
	out, err := d.do(ctx, "GetRun", runID, func() (any, error) { return d.driver.GetRun(ctx, runID) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.Run), nil
}

// ListRuns lists runs, optionally filtered by job ID (0 = all).
func (d *DataPlane) ListRuns(ctx context.Context, jobID int64) ([]driver.Run, error) {
	out, err := d.do(ctx, "ListRuns", jobID, func() (any, error) { return d.driver.ListRuns(ctx, jobID) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.Run), nil
}

// CancelRun cancels a job run.
func (d *DataPlane) CancelRun(ctx context.Context, runID int64) error {
	_, err := d.do(ctx, "CancelRun", runID, func() (any, error) { return nil, d.driver.CancelRun(ctx, runID) })

	return err
}

// GetRunOutput retrieves a run's output.
func (d *DataPlane) GetRunOutput(ctx context.Context, runID int64) (*driver.RunOutput, error) {
	out, err := d.do(ctx, "GetRunOutput", runID, func() (any, error) { return d.driver.GetRunOutput(ctx, runID) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.RunOutput), nil
}

// CreateClusterPolicy creates a cluster policy.
func (d *DataPlane) CreateClusterPolicy(ctx context.Context, cfg driver.ClusterPolicyConfig) (*driver.ClusterPolicy, error) {
	out, err := d.do(ctx, "CreateClusterPolicy", cfg, func() (any, error) { return d.driver.CreateClusterPolicy(ctx, cfg) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.ClusterPolicy), nil
}

// GetClusterPolicy retrieves a cluster policy by ID.
func (d *DataPlane) GetClusterPolicy(ctx context.Context, policyID string) (*driver.ClusterPolicy, error) {
	out, err := d.do(ctx, "GetClusterPolicy", policyID, func() (any, error) { return d.driver.GetClusterPolicy(ctx, policyID) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.ClusterPolicy), nil
}

// EditClusterPolicy updates a cluster policy.
func (d *DataPlane) EditClusterPolicy(ctx context.Context, policyID string, cfg driver.ClusterPolicyConfig) error {
	_, err := d.do(ctx, "EditClusterPolicy", policyID, func() (any, error) {
		return nil, d.driver.EditClusterPolicy(ctx, policyID, cfg)
	})

	return err
}

// DeleteClusterPolicy deletes a cluster policy by ID.
func (d *DataPlane) DeleteClusterPolicy(ctx context.Context, policyID string) error {
	_, err := d.do(ctx, "DeleteClusterPolicy", policyID, func() (any, error) {
		return nil, d.driver.DeleteClusterPolicy(ctx, policyID)
	})

	return err
}

// ListClusterPolicies lists all cluster policies.
func (d *DataPlane) ListClusterPolicies(ctx context.Context) ([]driver.ClusterPolicy, error) {
	out, err := d.do(ctx, "ListClusterPolicies", nil, func() (any, error) { return d.driver.ListClusterPolicies(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.ClusterPolicy), nil
}

// InstallLibraries installs libraries on a cluster.
func (d *DataPlane) InstallLibraries(ctx context.Context, clusterID string, libs []driver.LibrarySpec) error {
	_, err := d.do(ctx, "InstallLibraries", clusterID, func() (any, error) {
		return nil, d.driver.InstallLibraries(ctx, clusterID, libs)
	})

	return err
}

// UninstallLibraries marks libraries for removal on a cluster.
func (d *DataPlane) UninstallLibraries(ctx context.Context, clusterID string, libs []driver.LibrarySpec) error {
	_, err := d.do(ctx, "UninstallLibraries", clusterID, func() (any, error) {
		return nil, d.driver.UninstallLibraries(ctx, clusterID, libs)
	})

	return err
}

// ClusterLibraryStatuses returns the library statuses for one cluster.
func (d *DataPlane) ClusterLibraryStatuses(ctx context.Context, clusterID string) ([]driver.LibraryStatus, error) {
	out, err := d.do(ctx, "ClusterLibraryStatuses", clusterID, func() (any, error) {
		return d.driver.ClusterLibraryStatuses(ctx, clusterID)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.LibraryStatus), nil
}

// AllClusterLibraryStatuses returns library statuses across all clusters.
func (d *DataPlane) AllClusterLibraryStatuses(ctx context.Context) ([]driver.ClusterLibraryStatuses, error) {
	out, err := d.do(ctx, "AllClusterLibraryStatuses", nil, func() (any, error) {
		return d.driver.AllClusterLibraryStatuses(ctx)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.ClusterLibraryStatuses), nil
}

// ResizeCluster changes a cluster's worker count or autoscale bounds.
func (d *DataPlane) ResizeCluster(ctx context.Context, id string, numWorkers, autoscaleMin, autoscaleMax int32) error {
	_, err := d.do(ctx, "ResizeCluster", id, func() (any, error) {
		return nil, d.driver.ResizeCluster(ctx, id, numWorkers, autoscaleMin, autoscaleMax)
	})

	return err
}

// PinCluster pins a cluster.
func (d *DataPlane) PinCluster(ctx context.Context, id string) error {
	_, err := d.do(ctx, "PinCluster", id, func() (any, error) { return nil, d.driver.PinCluster(ctx, id) })

	return err
}

// UnpinCluster unpins a cluster.
func (d *DataPlane) UnpinCluster(ctx context.Context, id string) error {
	_, err := d.do(ctx, "UnpinCluster", id, func() (any, error) { return nil, d.driver.UnpinCluster(ctx, id) })

	return err
}

// ListNodeTypes returns the available node-type catalog.
func (d *DataPlane) ListNodeTypes(ctx context.Context) ([]driver.NodeType, error) {
	out, err := d.do(ctx, "ListNodeTypes", nil, func() (any, error) { return d.driver.ListNodeTypes(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.NodeType), nil
}

// ListSparkVersions returns the available runtime versions.
func (d *DataPlane) ListSparkVersions(ctx context.Context) ([]driver.SparkVersion, error) {
	out, err := d.do(ctx, "ListSparkVersions", nil, func() (any, error) { return d.driver.ListSparkVersions(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.SparkVersion), nil
}

// ListZones returns the availability zones and the default zone.
func (d *DataPlane) ListZones(ctx context.Context) (zones []string, defaultZone string, err error) {
	type zonesResult struct {
		zones       []string
		defaultZone string
	}

	out, err := d.do(ctx, "ListZones", nil, func() (any, error) {
		zs, def, zErr := d.driver.ListZones(ctx)

		return zonesResult{zones: zs, defaultZone: def}, zErr
	})
	if err != nil {
		return nil, "", err
	}

	res := out.(zonesResult)

	return res.zones, res.defaultZone, nil
}

// SubmitRun submits a one-time run and returns its ID.
func (d *DataPlane) SubmitRun(ctx context.Context, runName string) (int64, error) {
	out, err := d.do(ctx, "SubmitRun", runName, func() (any, error) { return d.driver.SubmitRun(ctx, runName) })
	if err != nil {
		return 0, err
	}

	return out.(int64), nil
}

// CancelAllRuns cancels all runs for a job.
func (d *DataPlane) CancelAllRuns(ctx context.Context, jobID int64) error {
	_, err := d.do(ctx, "CancelAllRuns", jobID, func() (any, error) { return nil, d.driver.CancelAllRuns(ctx, jobID) })

	return err
}

// DeleteRun deletes a run by ID.
func (d *DataPlane) DeleteRun(ctx context.Context, runID int64) error {
	_, err := d.do(ctx, "DeleteRun", runID, func() (any, error) { return nil, d.driver.DeleteRun(ctx, runID) })

	return err
}

// RepairRun repairs a run and returns the repair ID.
func (d *DataPlane) RepairRun(ctx context.Context, runID int64) (int64, error) {
	out, err := d.do(ctx, "RepairRun", runID, func() (any, error) { return d.driver.RepairRun(ctx, runID) })
	if err != nil {
		return 0, err
	}

	return out.(int64), nil
}
