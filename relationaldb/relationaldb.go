// Package relationaldb provides a portable relational-database service API
// (RDS, Cloud SQL, Azure SQL) layered on top of driver.RelationalDB. It threads
// recording, metrics, rate-limit, error-injection and latency through every
// call so consumers get the same cross-cutting story the other portable
// services have.
package relationaldb

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
	"github.com/stackshy/cloudemu/relationaldb/driver"
)

// DB is the portable handle around a relational-database driver.
type DB struct {
	driver   driver.RelationalDB
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

// NewDB wraps d with the supplied options.
func NewDB(d driver.RelationalDB, opts ...Option) *DB {
	db := &DB{driver: d}
	for _, opt := range opts {
		opt(db)
	}

	return db
}

// Option configures a DB.
type Option func(*DB)

// WithRecorder attaches a call recorder.
func WithRecorder(r *recorder.Recorder) Option { return func(db *DB) { db.recorder = r } }

// WithMetrics attaches a metrics collector.
func WithMetrics(m *metrics.Collector) Option { return func(db *DB) { db.metrics = m } }

// WithRateLimiter attaches a rate limiter.
func WithRateLimiter(l *ratelimit.Limiter) Option { return func(db *DB) { db.limiter = l } }

// WithErrorInjection attaches an error injector.
func WithErrorInjection(i *inject.Injector) Option { return func(db *DB) { db.injector = i } }

// WithLatency adds artificial per-call latency.
func WithLatency(d time.Duration) Option { return func(db *DB) { db.latency = d } }

func (db *DB) do(_ context.Context, op string, input any, fn func() (any, error)) (any, error) {
	start := time.Now()

	if db.injector != nil {
		if err := db.injector.Check("relationaldb", op); err != nil {
			db.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if db.limiter != nil {
		if err := db.limiter.Allow(); err != nil {
			db.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if db.latency > 0 {
		time.Sleep(db.latency)
	}

	out, err := fn()
	dur := time.Since(start)

	if db.metrics != nil {
		labels := map[string]string{"service": "relationaldb", "operation": op}
		db.metrics.Counter("calls_total", 1, labels)
		db.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			db.metrics.Counter("errors_total", 1, labels)
		}
	}

	db.rec(op, input, out, err, dur)

	return out, err
}

func (db *DB) rec(op string, input, output any, err error, dur time.Duration) {
	if db.recorder != nil {
		db.recorder.Record("relationaldb", op, input, output, err, dur)
	}
}

// CreateInstance creates a new database instance.
//
//nolint:gocritic // cfg is a value type to match the driver interface.
func (db *DB) CreateInstance(ctx context.Context, cfg driver.InstanceConfig) (*driver.Instance, error) {
	out, err := db.do(ctx, "CreateInstance", cfg, func() (any, error) { return db.driver.CreateInstance(ctx, cfg) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.Instance), nil
}

// DescribeInstances returns instances matching ids (or all if empty).
func (db *DB) DescribeInstances(ctx context.Context, ids []string) ([]driver.Instance, error) {
	out, err := db.do(ctx, "DescribeInstances", ids, func() (any, error) { return db.driver.DescribeInstances(ctx, ids) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.Instance), nil
}

// ModifyInstance applies the supplied changes to an instance.
func (db *DB) ModifyInstance(
	ctx context.Context, id string, input driver.ModifyInstanceInput,
) (*driver.Instance, error) {
	out, err := db.do(ctx, "ModifyInstance", input, func() (any, error) {
		return db.driver.ModifyInstance(ctx, id, input)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.Instance), nil
}

// DeleteInstance removes an instance.
func (db *DB) DeleteInstance(ctx context.Context, id string) error {
	_, err := db.do(ctx, "DeleteInstance", id, func() (any, error) {
		return nil, db.driver.DeleteInstance(ctx, id)
	})

	return err
}

// StartInstance moves a stopped instance to available.
func (db *DB) StartInstance(ctx context.Context, id string) error {
	_, err := db.do(ctx, "StartInstance", id, func() (any, error) {
		return nil, db.driver.StartInstance(ctx, id)
	})

	return err
}

// StopInstance moves an available instance to stopped.
func (db *DB) StopInstance(ctx context.Context, id string) error {
	_, err := db.do(ctx, "StopInstance", id, func() (any, error) {
		return nil, db.driver.StopInstance(ctx, id)
	})

	return err
}

// RebootInstance cycles an instance through rebooting → available.
func (db *DB) RebootInstance(ctx context.Context, id string) error {
	_, err := db.do(ctx, "RebootInstance", id, func() (any, error) {
		return nil, db.driver.RebootInstance(ctx, id)
	})

	return err
}

// CreateCluster creates an Aurora-style cluster.
//
//nolint:gocritic // cfg is a value type to match the driver interface.
func (db *DB) CreateCluster(ctx context.Context, cfg driver.ClusterConfig) (*driver.Cluster, error) {
	out, err := db.do(ctx, "CreateCluster", cfg, func() (any, error) { return db.driver.CreateCluster(ctx, cfg) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.Cluster), nil
}

// DescribeClusters returns clusters matching ids (or all if empty).
func (db *DB) DescribeClusters(ctx context.Context, ids []string) ([]driver.Cluster, error) {
	out, err := db.do(ctx, "DescribeClusters", ids, func() (any, error) { return db.driver.DescribeClusters(ctx, ids) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.Cluster), nil
}

// ModifyCluster applies changes to a cluster.
func (db *DB) ModifyCluster(
	ctx context.Context, id string, input driver.ModifyInstanceInput,
) (*driver.Cluster, error) {
	out, err := db.do(ctx, "ModifyCluster", input, func() (any, error) {
		return db.driver.ModifyCluster(ctx, id, input)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.Cluster), nil
}

// DeleteCluster removes a cluster.
func (db *DB) DeleteCluster(ctx context.Context, id string) error {
	_, err := db.do(ctx, "DeleteCluster", id, func() (any, error) {
		return nil, db.driver.DeleteCluster(ctx, id)
	})

	return err
}

// StartCluster moves a stopped cluster to available.
func (db *DB) StartCluster(ctx context.Context, id string) error {
	_, err := db.do(ctx, "StartCluster", id, func() (any, error) {
		return nil, db.driver.StartCluster(ctx, id)
	})

	return err
}

// StopCluster moves an available cluster to stopped.
func (db *DB) StopCluster(ctx context.Context, id string) error {
	_, err := db.do(ctx, "StopCluster", id, func() (any, error) {
		return nil, db.driver.StopCluster(ctx, id)
	})

	return err
}

// CreateSnapshot snapshots an instance.
func (db *DB) CreateSnapshot(ctx context.Context, cfg driver.SnapshotConfig) (*driver.Snapshot, error) {
	out, err := db.do(ctx, "CreateSnapshot", cfg, func() (any, error) { return db.driver.CreateSnapshot(ctx, cfg) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.Snapshot), nil
}

// DescribeSnapshots returns snapshots matching ids and/or instance.
func (db *DB) DescribeSnapshots(
	ctx context.Context, ids []string, instanceID string,
) ([]driver.Snapshot, error) {
	out, err := db.do(ctx, "DescribeSnapshots", ids, func() (any, error) {
		return db.driver.DescribeSnapshots(ctx, ids, instanceID)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.Snapshot), nil
}

// DeleteSnapshot removes an instance snapshot.
func (db *DB) DeleteSnapshot(ctx context.Context, id string) error {
	_, err := db.do(ctx, "DeleteSnapshot", id, func() (any, error) {
		return nil, db.driver.DeleteSnapshot(ctx, id)
	})

	return err
}

// RestoreInstanceFromSnapshot creates a new instance from a snapshot.
func (db *DB) RestoreInstanceFromSnapshot(
	ctx context.Context, input driver.RestoreInstanceInput,
) (*driver.Instance, error) {
	out, err := db.do(ctx, "RestoreInstanceFromSnapshot", input, func() (any, error) {
		return db.driver.RestoreInstanceFromSnapshot(ctx, input)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.Instance), nil
}

// CreateClusterSnapshot snapshots a cluster.
func (db *DB) CreateClusterSnapshot(
	ctx context.Context, cfg driver.ClusterSnapshotConfig,
) (*driver.ClusterSnapshot, error) {
	out, err := db.do(ctx, "CreateClusterSnapshot", cfg, func() (any, error) {
		return db.driver.CreateClusterSnapshot(ctx, cfg)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.ClusterSnapshot), nil
}

// DescribeClusterSnapshots returns cluster snapshots matching ids and/or cluster.
func (db *DB) DescribeClusterSnapshots(
	ctx context.Context, ids []string, clusterID string,
) ([]driver.ClusterSnapshot, error) {
	out, err := db.do(ctx, "DescribeClusterSnapshots", ids, func() (any, error) {
		return db.driver.DescribeClusterSnapshots(ctx, ids, clusterID)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.ClusterSnapshot), nil
}

// DeleteClusterSnapshot removes a cluster snapshot.
func (db *DB) DeleteClusterSnapshot(ctx context.Context, id string) error {
	_, err := db.do(ctx, "DeleteClusterSnapshot", id, func() (any, error) {
		return nil, db.driver.DeleteClusterSnapshot(ctx, id)
	})

	return err
}

// RestoreClusterFromSnapshot creates a new cluster from a cluster snapshot.
func (db *DB) RestoreClusterFromSnapshot(
	ctx context.Context, input driver.RestoreClusterInput,
) (*driver.Cluster, error) {
	out, err := db.do(ctx, "RestoreClusterFromSnapshot", input, func() (any, error) {
		return db.driver.RestoreClusterFromSnapshot(ctx, input)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.Cluster), nil
}
