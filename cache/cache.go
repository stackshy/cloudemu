// Package cache provides a portable cache API with cross-cutting concerns.
package cache

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/cache/driver"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
)

// Cache is the portable cache type wrapping a driver with cross-cutting concerns.
type Cache struct {
	driver   driver.Cache
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

// NewCache creates a new portable Cache wrapping the given driver.
func NewCache(d driver.Cache, opts ...Option) *Cache {
	c := &Cache{driver: d}
	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Option configures a portable Cache.
type Option func(*Cache)

// WithRecorder sets the recorder.
func WithRecorder(r *recorder.Recorder) Option { return func(c *Cache) { c.recorder = r } }

// WithMetrics sets the metrics collector.
func WithMetrics(m *metrics.Collector) Option { return func(c *Cache) { c.metrics = m } }

// WithRateLimiter sets the rate limiter.
func WithRateLimiter(l *ratelimit.Limiter) Option { return func(c *Cache) { c.limiter = l } }

// WithErrorInjection sets the error injector.
func WithErrorInjection(i *inject.Injector) Option { return func(c *Cache) { c.injector = i } }

// WithLatency sets simulated latency.
func WithLatency(d time.Duration) Option { return func(c *Cache) { c.latency = d } }

func (c *Cache) do(_ context.Context, op string, input any, fn func() (any, error)) (any, error) {
	start := time.Now()

	if c.injector != nil {
		if err := c.injector.Check("cache", op); err != nil {
			c.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if c.limiter != nil {
		if err := c.limiter.Allow(); err != nil {
			c.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if c.latency > 0 {
		time.Sleep(c.latency)
	}

	out, err := fn()
	dur := time.Since(start)

	if c.metrics != nil {
		labels := map[string]string{"service": "cache", "operation": op}
		c.metrics.Counter("calls_total", 1, labels)
		c.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			c.metrics.Counter("errors_total", 1, labels)
		}
	}

	c.rec(op, input, out, err, dur)

	return out, err
}

func (c *Cache) rec(op string, input, output any, err error, dur time.Duration) {
	if c.recorder != nil {
		c.recorder.Record("cache", op, input, output, err, dur)
	}
}

// CreateCache creates a new cache instance.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (c *Cache) CreateCache(ctx context.Context, config driver.CacheConfig) (*driver.CacheInfo, error) {
	out, err := c.do(ctx, "CreateCache", config, func() (any, error) { return c.driver.CreateCache(ctx, config) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.CacheInfo), nil
}

// DeleteCache deletes a cache instance.
func (c *Cache) DeleteCache(ctx context.Context, name string) error {
	_, err := c.do(ctx, "DeleteCache", name, func() (any, error) { return nil, c.driver.DeleteCache(ctx, name) })
	return err
}

// GetCache retrieves cache instance info.
func (c *Cache) GetCache(ctx context.Context, name string) (*driver.CacheInfo, error) {
	out, err := c.do(ctx, "GetCache", name, func() (any, error) { return c.driver.GetCache(ctx, name) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.CacheInfo), nil
}

// ListCaches lists all cache instances.
func (c *Cache) ListCaches(ctx context.Context) ([]driver.CacheInfo, error) {
	out, err := c.do(ctx, "ListCaches", nil, func() (any, error) { return c.driver.ListCaches(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.CacheInfo), nil
}

// Set stores a value in the cache.
func (c *Cache) Set(ctx context.Context, cacheName, key string, value []byte, ttl time.Duration) error {
	_, err := c.do(ctx, "Set", map[string]string{"cache": cacheName, "key": key}, func() (any, error) {
		return nil, c.driver.Set(ctx, cacheName, key, value, ttl)
	})

	return err
}

// Get retrieves a value from the cache.
func (c *Cache) Get(ctx context.Context, cacheName, key string) (*driver.Item, error) {
	out, err := c.do(ctx, "Get", map[string]string{"cache": cacheName, "key": key}, func() (any, error) {
		return c.driver.Get(ctx, cacheName, key)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.Item), nil
}

// Delete removes a value from the cache.
func (c *Cache) Delete(ctx context.Context, cacheName, key string) error {
	_, err := c.do(ctx, "Delete", map[string]string{"cache": cacheName, "key": key}, func() (any, error) {
		return nil, c.driver.Delete(ctx, cacheName, key)
	})

	return err
}

// Keys returns all keys matching the given pattern.
func (c *Cache) Keys(ctx context.Context, cacheName, pattern string) ([]string, error) {
	out, err := c.do(ctx, "Keys", map[string]string{"cache": cacheName, "pattern": pattern}, func() (any, error) {
		return c.driver.Keys(ctx, cacheName, pattern)
	})
	if err != nil {
		return nil, err
	}

	return out.([]string), nil
}

// FlushAll removes all items from the cache.
func (c *Cache) FlushAll(ctx context.Context, cacheName string) error {
	_, err := c.do(ctx, "FlushAll", cacheName, func() (any, error) { return nil, c.driver.FlushAll(ctx, cacheName) })
	return err
}
