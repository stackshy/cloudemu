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
