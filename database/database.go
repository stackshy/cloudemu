// Package database provides a portable database API with cross-cutting concerns.
package database

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/database/driver"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
)

// Database is the portable database type wrapping a driver.
type Database struct {
	driver   driver.Database
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

// NewDatabase creates a new portable Database.
func NewDatabase(d driver.Database, opts ...Option) *Database {
	db := &Database{driver: d}
	for _, opt := range opts {
		opt(db)
	}

	return db
}

// Option configures a portable Database.
type Option func(*Database)

func WithRecorder(r *recorder.Recorder) Option     { return func(d *Database) { d.recorder = r } }
func WithMetrics(m *metrics.Collector) Option      { return func(d *Database) { d.metrics = m } }
func WithRateLimiter(l *ratelimit.Limiter) Option  { return func(d *Database) { d.limiter = l } }
func WithErrorInjection(i *inject.Injector) Option { return func(d *Database) { d.injector = i } }
func WithLatency(dur time.Duration) Option         { return func(d *Database) { d.latency = dur } }

func (db *Database) do(_ context.Context, op string, input any, fn func() (any, error)) (any, error) {
	start := time.Now()

	if db.injector != nil {
		if err := db.injector.Check("database", op); err != nil {
			db.record(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if db.limiter != nil {
		if err := db.limiter.Allow(); err != nil {
			db.record(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if db.latency > 0 {
		time.Sleep(db.latency)
	}

	out, err := fn()
	dur := time.Since(start)

	if db.metrics != nil {
		labels := map[string]string{"service": "database", "operation": op}
		db.metrics.Counter("calls_total", 1, labels)
		db.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			db.metrics.Counter("errors_total", 1, labels)
		}
	}

	db.record(op, input, out, err, dur)

	return out, err
}

func (db *Database) record(op string, input, output any, err error, dur time.Duration) {
	if db.recorder != nil {
		db.recorder.Record("database", op, input, output, err, dur)
	}
}

func (db *Database) CreateTable(ctx context.Context, config driver.TableConfig) error {
	_, err := db.do(ctx, "CreateTable", config, func() (any, error) { return nil, db.driver.CreateTable(ctx, config) })
	return err
}

func (db *Database) DeleteTable(ctx context.Context, name string) error {
	_, err := db.do(ctx, "DeleteTable", name, func() (any, error) { return nil, db.driver.DeleteTable(ctx, name) })
	return err
}

func (db *Database) DescribeTable(ctx context.Context, name string) (*driver.TableConfig, error) {
	out, err := db.do(ctx, "DescribeTable", name, func() (any, error) { return db.driver.DescribeTable(ctx, name) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.TableConfig), nil
}

func (db *Database) ListTables(ctx context.Context) ([]string, error) {
	out, err := db.do(ctx, "ListTables", nil, func() (any, error) { return db.driver.ListTables(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]string), nil
}

func (db *Database) PutItem(ctx context.Context, table string, item map[string]any) error {
	_, err := db.do(ctx, "PutItem", map[string]any{"table": table}, func() (any, error) { return nil, db.driver.PutItem(ctx, table, item) })
	return err
}

func (db *Database) GetItem(ctx context.Context, table string, key map[string]any) (map[string]any, error) {
	out, err := db.do(ctx, "GetItem", map[string]any{"table": table}, func() (any, error) {
		return db.driver.GetItem(ctx, table, key)
	})

	if err != nil {
		return nil, err
	}

	if out == nil {
		return nil, nil
	}

	return out.(map[string]any), nil
}

func (db *Database) DeleteItem(ctx context.Context, table string, key map[string]any) error {
	_, err := db.do(ctx, "DeleteItem", map[string]any{"table": table}, func() (any, error) {
		return nil, db.driver.DeleteItem(ctx, table, key)
	})

	return err
}

//nolint:gocritic // input passed by value to match driver.Database interface pattern
func (db *Database) Query(ctx context.Context, input driver.QueryInput) (*driver.QueryResult, error) {
	out, err := db.do(ctx, "Query", input, func() (any, error) { return db.driver.Query(ctx, input) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.QueryResult), nil
}

func (db *Database) Scan(ctx context.Context, input driver.ScanInput) (*driver.QueryResult, error) {
	out, err := db.do(ctx, "Scan", input, func() (any, error) { return db.driver.Scan(ctx, input) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.QueryResult), nil
}

func (db *Database) BatchPutItems(ctx context.Context, table string, items []map[string]any) error {
	_, err := db.do(ctx, "BatchPutItems", map[string]any{"table": table}, func() (any, error) {
		return nil, db.driver.BatchPutItems(ctx, table, items)
	})

	return err
}

func (db *Database) BatchGetItems(ctx context.Context, table string, keys []map[string]any) ([]map[string]any, error) {
	out, err := db.do(ctx, "BatchGetItems", map[string]any{"table": table}, func() (any, error) {
		return db.driver.BatchGetItems(ctx, table, keys)
	})

	if err != nil {
		return nil, err
	}

	return out.([]map[string]any), nil
}

func (db *Database) UpdateTTL(ctx context.Context, table string, cfg driver.TTLConfig) error {
	_, err := db.do(ctx, "UpdateTTL", map[string]any{"table": table}, func() (any, error) {
		return nil, db.driver.UpdateTTL(ctx, table, cfg)
	})

	return err
}

func (db *Database) DescribeTTL(ctx context.Context, table string) (*driver.TTLConfig, error) {
	out, err := db.do(ctx, "DescribeTTL", table, func() (any, error) {
		return db.driver.DescribeTTL(ctx, table)
	})

	if err != nil {
		return nil, err
	}

	return out.(*driver.TTLConfig), nil
}

func (db *Database) UpdateStreamConfig(ctx context.Context, table string, cfg driver.StreamConfig) error {
	_, err := db.do(ctx, "UpdateStreamConfig", map[string]any{"table": table}, func() (any, error) {
		return nil, db.driver.UpdateStreamConfig(ctx, table, cfg)
	})

	return err
}

func (db *Database) GetStreamRecords(
	ctx context.Context, table string, limit int, token string,
) (*driver.StreamIterator, error) {
	out, err := db.do(ctx, "GetStreamRecords", map[string]any{"table": table}, func() (any, error) {
		return db.driver.GetStreamRecords(ctx, table, limit, token)
	})

	if err != nil {
		return nil, err
	}

	return out.(*driver.StreamIterator), nil
}

func (db *Database) TransactWriteItems(
	ctx context.Context, table string, puts []map[string]any, deletes []map[string]any,
) error {
	_, err := db.do(ctx, "TransactWriteItems", map[string]any{"table": table}, func() (any, error) {
		return nil, db.driver.TransactWriteItems(ctx, table, puts, deletes)
	})

	return err
}
