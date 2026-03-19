// Package storage provides a portable storage bucket API with cross-cutting concerns.
package storage

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
	"github.com/stackshy/cloudemu/storage/driver"
)

// Bucket is the portable storage type wrapping a driver with cross-cutting concerns.
type Bucket struct {
	driver   driver.Bucket
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

// NewBucket creates a new portable Bucket wrapping the given driver.
func NewBucket(d driver.Bucket, opts ...Option) *Bucket {
	b := &Bucket{driver: d}
	for _, opt := range opts {
		opt(b)
	}

	return b
}

// Option configures a portable Bucket.
type Option func(*Bucket)

// WithRecorder sets the recorder.
func WithRecorder(r *recorder.Recorder) Option { return func(b *Bucket) { b.recorder = r } }

// WithMetrics sets the metrics collector.
func WithMetrics(m *metrics.Collector) Option { return func(b *Bucket) { b.metrics = m } }

// WithRateLimiter sets the rate limiter.
func WithRateLimiter(l *ratelimit.Limiter) Option { return func(b *Bucket) { b.limiter = l } }

// WithErrorInjection sets the error injector.
func WithErrorInjection(i *inject.Injector) Option { return func(b *Bucket) { b.injector = i } }

// WithLatency sets simulated latency.
func WithLatency(d time.Duration) Option { return func(b *Bucket) { b.latency = d } }

func (b *Bucket) do(_ context.Context, op string, input any, fn func() (any, error)) (any, error) {
	start := time.Now()

	if b.injector != nil {
		if err := b.injector.Check("storage", op); err != nil {
			b.record(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if b.limiter != nil {
		if err := b.limiter.Allow(); err != nil {
			b.record(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if b.latency > 0 {
		time.Sleep(b.latency)
	}

	out, err := fn()
	dur := time.Since(start)

	if b.metrics != nil {
		labels := map[string]string{"service": "storage", "operation": op}
		b.metrics.Counter("calls_total", 1, labels)
		b.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			b.metrics.Counter("errors_total", 1, labels)
		}
	}

	b.record(op, input, out, err, dur)

	return out, err
}

func (b *Bucket) record(op string, input, output any, err error, dur time.Duration) {
	if b.recorder != nil {
		b.recorder.Record("storage", op, input, output, err, dur)
	}
}

// CreateBucket creates a new storage bucket.
func (b *Bucket) CreateBucket(ctx context.Context, name string) error {
	_, err := b.do(ctx, "CreateBucket", name, func() (any, error) {
		return nil, b.driver.CreateBucket(ctx, name)
	})

	return err
}

// DeleteBucket deletes a storage bucket.
func (b *Bucket) DeleteBucket(ctx context.Context, name string) error {
	_, err := b.do(ctx, "DeleteBucket", name, func() (any, error) {
		return nil, b.driver.DeleteBucket(ctx, name)
	})

	return err
}

// ListBuckets lists all buckets.
func (b *Bucket) ListBuckets(ctx context.Context) ([]driver.BucketInfo, error) {
	out, err := b.do(ctx, "ListBuckets", nil, func() (any, error) {
		return b.driver.ListBuckets(ctx)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.BucketInfo), nil
}

// PutObject stores an object in a bucket.
func (b *Bucket) PutObject(ctx context.Context, bucket, key string, data []byte, contentType string, metadata map[string]string) error {
	_, err := b.do(ctx, "PutObject", map[string]string{"bucket": bucket, "key": key}, func() (any, error) {
		return nil, b.driver.PutObject(ctx, bucket, key, data, contentType, metadata)
	})

	return err
}

// GetObject retrieves an object from a bucket.
func (b *Bucket) GetObject(ctx context.Context, bucket, key string) (*driver.Object, error) {
	out, err := b.do(ctx, "GetObject", map[string]string{"bucket": bucket, "key": key}, func() (any, error) {
		return b.driver.GetObject(ctx, bucket, key)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.Object), nil
}

// DeleteObject deletes an object from a bucket.
func (b *Bucket) DeleteObject(ctx context.Context, bucket, key string) error {
	_, err := b.do(ctx, "DeleteObject", map[string]string{"bucket": bucket, "key": key}, func() (any, error) {
		return nil, b.driver.DeleteObject(ctx, bucket, key)
	})

	return err
}

// HeadObject retrieves object metadata without the body.
func (b *Bucket) HeadObject(ctx context.Context, bucket, key string) (*driver.ObjectInfo, error) {
	out, err := b.do(ctx, "HeadObject", map[string]string{"bucket": bucket, "key": key}, func() (any, error) {
		return b.driver.HeadObject(ctx, bucket, key)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.ObjectInfo), nil
}

// ListObjects lists objects in a bucket.
func (b *Bucket) ListObjects(ctx context.Context, bucket string, opts driver.ListOptions) (*driver.ListResult, error) {
	out, err := b.do(ctx, "ListObjects", map[string]any{"bucket": bucket, "opts": opts}, func() (any, error) {
		return b.driver.ListObjects(ctx, bucket, opts)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.ListResult), nil
}

// CopyObject copies an object between buckets.
func (b *Bucket) CopyObject(ctx context.Context, dstBucket, dstKey string, src driver.CopySource) error {
	_, err := b.do(ctx, "CopyObject", map[string]any{"dstBucket": dstBucket, "dstKey": dstKey, "src": src}, func() (any, error) {
		return nil, b.driver.CopyObject(ctx, dstBucket, dstKey, src)
	})

	return err
}
