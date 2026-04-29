package chaos

import (
	"context"
	"time"

	cachedriver "github.com/stackshy/cloudemu/cache/driver"
)

// chaosCache wraps a cache driver. Hot-path: K/V ops and atomic counters.
// Cache instance management and TTL admin delegate through.
type chaosCache struct {
	cachedriver.Cache
	engine *Engine
}

// WrapCache returns a cache driver that consults engine on data-plane calls.
func WrapCache(inner cachedriver.Cache, engine *Engine) cachedriver.Cache {
	return &chaosCache{Cache: inner, engine: engine}
}

func (c *chaosCache) Set(ctx context.Context, cacheName, key string, value []byte, ttl time.Duration) error {
	if err := applyChaos(ctx, c.engine, "cache", "Set"); err != nil {
		return err
	}

	return c.Cache.Set(ctx, cacheName, key, value, ttl)
}

func (c *chaosCache) Get(ctx context.Context, cacheName, key string) (*cachedriver.Item, error) {
	if err := applyChaos(ctx, c.engine, "cache", "Get"); err != nil {
		return nil, err
	}

	return c.Cache.Get(ctx, cacheName, key)
}

func (c *chaosCache) Delete(ctx context.Context, cacheName, key string) error {
	if err := applyChaos(ctx, c.engine, "cache", "Delete"); err != nil {
		return err
	}

	return c.Cache.Delete(ctx, cacheName, key)
}

func (c *chaosCache) Keys(ctx context.Context, cacheName, pattern string) ([]string, error) {
	if err := applyChaos(ctx, c.engine, "cache", "Keys"); err != nil {
		return nil, err
	}

	return c.Cache.Keys(ctx, cacheName, pattern)
}

func (c *chaosCache) Incr(ctx context.Context, cacheName, key string) (int64, error) {
	if err := applyChaos(ctx, c.engine, "cache", "Incr"); err != nil {
		return 0, err
	}

	return c.Cache.Incr(ctx, cacheName, key)
}

func (c *chaosCache) IncrBy(ctx context.Context, cacheName, key string, delta int64) (int64, error) {
	if err := applyChaos(ctx, c.engine, "cache", "IncrBy"); err != nil {
		return 0, err
	}

	return c.Cache.IncrBy(ctx, cacheName, key, delta)
}

func (c *chaosCache) Decr(ctx context.Context, cacheName, key string) (int64, error) {
	if err := applyChaos(ctx, c.engine, "cache", "Decr"); err != nil {
		return 0, err
	}

	return c.Cache.Decr(ctx, cacheName, key)
}

func (c *chaosCache) DecrBy(ctx context.Context, cacheName, key string, delta int64) (int64, error) {
	if err := applyChaos(ctx, c.engine, "cache", "DecrBy"); err != nil {
		return 0, err
	}

	return c.Cache.DecrBy(ctx, cacheName, key, delta)
}
