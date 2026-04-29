package chaos_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu"
	"github.com/stackshy/cloudemu/chaos"
	"github.com/stackshy/cloudemu/config"
	cachedriver "github.com/stackshy/cloudemu/cache/driver"
)

const (
	chaosTestCacheName = "c"
	chaosTestCacheKey  = "k"
)

func newChaosCache(t *testing.T) (cachedriver.Cache, *chaos.Engine) {
	t.Helper()

	e := chaos.New(config.RealClock{})
	t.Cleanup(e.Stop)
	c := chaos.WrapCache(cloudemu.NewAWS().ElastiCache, e)
	_, _ = c.CreateCache(context.Background(), cachedriver.CacheConfig{Name: chaosTestCacheName, Engine: "redis"})

	return c, e
}

func TestWrapCacheSetChaos(t *testing.T) {
	c, e := newChaosCache(t)
	ctx := context.Background()

	if err := c.Set(ctx, chaosTestCacheName, chaosTestCacheKey, []byte("v"), time.Minute); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("cache", time.Hour))

	if err := c.Set(ctx, chaosTestCacheName, chaosTestCacheKey, []byte("v"), time.Minute); err == nil {
		t.Error("expected chaos error on Set")
	}
}

func TestWrapCacheGetChaos(t *testing.T) {
	c, e := newChaosCache(t)
	ctx := context.Background()
	_ = c.Set(ctx, chaosTestCacheName, chaosTestCacheKey, []byte("v"), time.Minute)

	e.Apply(chaos.ServiceOutage("cache", time.Hour))

	if _, err := c.Get(ctx, chaosTestCacheName, chaosTestCacheKey); err == nil {
		t.Error("expected chaos error on Get")
	}
}

func TestWrapCacheDeleteChaos(t *testing.T) {
	c, e := newChaosCache(t)
	ctx := context.Background()
	_ = c.Set(ctx, chaosTestCacheName, chaosTestCacheKey, []byte("v"), time.Minute)

	e.Apply(chaos.ServiceOutage("cache", time.Hour))

	if err := c.Delete(ctx, chaosTestCacheName, chaosTestCacheKey); err == nil {
		t.Error("expected chaos error on Delete")
	}
}

func TestWrapCacheKeysChaos(t *testing.T) {
	c, e := newChaosCache(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("cache", time.Hour))

	if _, err := c.Keys(ctx, chaosTestCacheName, "*"); err == nil {
		t.Error("expected chaos error on Keys")
	}
}

func TestWrapCacheIncrChaos(t *testing.T) {
	c, e := newChaosCache(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("cache", time.Hour))

	if _, err := c.Incr(ctx, chaosTestCacheName, chaosTestCacheKey); err == nil {
		t.Error("expected chaos error on Incr")
	}
}

func TestWrapCacheIncrByChaos(t *testing.T) {
	c, e := newChaosCache(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("cache", time.Hour))

	if _, err := c.IncrBy(ctx, chaosTestCacheName, chaosTestCacheKey, 5); err == nil {
		t.Error("expected chaos error on IncrBy")
	}
}

func TestWrapCacheDecrChaos(t *testing.T) {
	c, e := newChaosCache(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("cache", time.Hour))

	if _, err := c.Decr(ctx, chaosTestCacheName, chaosTestCacheKey); err == nil {
		t.Error("expected chaos error on Decr")
	}
}

func TestWrapCacheDecrByChaos(t *testing.T) {
	c, e := newChaosCache(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("cache", time.Hour))

	if _, err := c.DecrBy(ctx, chaosTestCacheName, chaosTestCacheKey, 3); err == nil {
		t.Error("expected chaos error on DecrBy")
	}
}
