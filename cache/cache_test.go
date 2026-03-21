package cache

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/cache/driver"
	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/providers/aws/elasticache"
	"github.com/stackshy/cloudemu/recorder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestDriver() (driver.Cache, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return elasticache.New(opts), fc
}

func newTestCache(opts ...Option) (*Cache, *config.FakeClock) {
	d, fc := newTestDriver()
	return NewCache(d, opts...), fc
}

func setupCacheWithItem(t *testing.T, c *Cache) {
	t.Helper()

	ctx := context.Background()

	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "test-cache"})
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	err = c.Set(ctx, "test-cache", "key1", []byte("value1"), 0)
	if err != nil {
		t.Fatalf("failed to set item: %v", err)
	}
}

func TestNewCache(t *testing.T) {
	c, _ := newTestCache()

	require.NotNil(t, c)
	require.NotNil(t, c.driver)
}

func TestCreateCachePortable(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		info, err := c.CreateCache(ctx, driver.CacheConfig{Name: "my-cache"})
		require.NoError(t, err)
		assert.Equal(t, "my-cache", info.Name)
		assert.NotEmpty(t, info.Endpoint)
	})

	t.Run("empty name error", func(t *testing.T) {
		_, err := c.CreateCache(ctx, driver.CacheConfig{})
		require.Error(t, err)
	})
}

func TestDeleteCachePortable(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "del-cache"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		delErr := c.DeleteCache(ctx, "del-cache")
		require.NoError(t, delErr)
	})

	t.Run("not found", func(t *testing.T) {
		delErr := c.DeleteCache(ctx, "nonexistent")
		require.Error(t, delErr)
	})
}

func TestGetCachePortable(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "get-cache"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		info, getErr := c.GetCache(ctx, "get-cache")
		require.NoError(t, getErr)
		assert.Equal(t, "get-cache", info.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, getErr := c.GetCache(ctx, "nonexistent")
		require.Error(t, getErr)
	})
}

func TestListCachesPortable(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	caches, err := c.ListCaches(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, len(caches))

	_, err = c.CreateCache(ctx, driver.CacheConfig{Name: "a"})
	require.NoError(t, err)

	_, err = c.CreateCache(ctx, driver.CacheConfig{Name: "b"})
	require.NoError(t, err)

	caches, err = c.ListCaches(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, len(caches))
}

func TestSetGetDeletePortable(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	setupCacheWithItem(t, c)

	t.Run("get existing key", func(t *testing.T) {
		item, err := c.Get(ctx, "test-cache", "key1")
		require.NoError(t, err)
		assert.Equal(t, "key1", item.Key)
		assert.Equal(t, string([]byte("value1")), string(item.Value))
	})

	t.Run("delete key", func(t *testing.T) {
		err := c.Delete(ctx, "test-cache", "key1")
		require.NoError(t, err)

		_, getErr := c.Get(ctx, "test-cache", "key1")
		require.Error(t, getErr)
	})
}

func TestKeysPortable(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "keys-cache"})
	require.NoError(t, err)

	err = c.Set(ctx, "keys-cache", "user:1", []byte("a"), 0)
	require.NoError(t, err)

	err = c.Set(ctx, "keys-cache", "user:2", []byte("b"), 0)
	require.NoError(t, err)

	keys, err := c.Keys(ctx, "keys-cache", "user:*")
	require.NoError(t, err)
	assert.Equal(t, 2, len(keys))
}

func TestFlushAllPortable(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "flush-cache"})
	require.NoError(t, err)

	err = c.Set(ctx, "flush-cache", "k1", []byte("v1"), 0)
	require.NoError(t, err)

	err = c.FlushAll(ctx, "flush-cache")
	require.NoError(t, err)

	keys, err := c.Keys(ctx, "flush-cache", "*")
	require.NoError(t, err)
	assert.Equal(t, 0, len(keys))
}

func TestWithRecorder(t *testing.T) {
	rec := recorder.New()
	c, _ := newTestCache(WithRecorder(rec))
	ctx := context.Background()

	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "rec-cache"})
	require.NoError(t, err)

	err = c.Set(ctx, "rec-cache", "k", []byte("v"), 0)
	require.NoError(t, err)

	_, err = c.Get(ctx, "rec-cache", "k")
	require.NoError(t, err)

	totalCalls := rec.CallCount()
	assert.GreaterOrEqual(t, totalCalls, 3)

	createCalls := rec.CallCountFor("cache", "CreateCache")
	assert.Equal(t, 1, createCalls)

	setCalls := rec.CallCountFor("cache", "Set")
	assert.Equal(t, 1, setCalls)

	getCalls := rec.CallCountFor("cache", "Get")
	assert.Equal(t, 1, getCalls)
}

func TestWithRecorderOnError(t *testing.T) {
	rec := recorder.New()
	c, _ := newTestCache(WithRecorder(rec))
	ctx := context.Background()

	// This should fail and still be recorded.
	_, _ = c.GetCache(ctx, "nonexistent")

	totalCalls := rec.CallCount()
	assert.Equal(t, 1, totalCalls)

	last := rec.LastCall()
	require.NotNil(t, last, "expected a recorded call")
	assert.NotNil(t, last.Error, "expected recorded call to have an error")
}

func TestWithMetrics(t *testing.T) {
	mc := metrics.NewCollector()
	c, _ := newTestCache(WithMetrics(mc))
	ctx := context.Background()

	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "met-cache"})
	require.NoError(t, err)

	err = c.Set(ctx, "met-cache", "k", []byte("v"), 0)
	require.NoError(t, err)

	_, err = c.Get(ctx, "met-cache", "k")
	require.NoError(t, err)

	q := metrics.NewQuery(mc)

	callsCount := q.ByName("calls_total").Count()
	assert.GreaterOrEqual(t, callsCount, 3)

	durCount := q.ByName("call_duration").Count()
	assert.GreaterOrEqual(t, durCount, 3)
}

func TestWithMetricsOnError(t *testing.T) {
	mc := metrics.NewCollector()
	c, _ := newTestCache(WithMetrics(mc))
	ctx := context.Background()

	// This should fail.
	_, _ = c.GetCache(ctx, "nonexistent")

	q := metrics.NewQuery(mc)

	errCount := q.ByName("errors_total").Count()
	assert.Equal(t, 1, errCount)
}

func TestWithErrorInjection(t *testing.T) {
	inj := inject.NewInjector()
	c, _ := newTestCache(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("injected failure")
	inj.Set("cache", "CreateCache", injectedErr, inject.Always{})

	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "fail-cache"})
	require.Error(t, err)
	assert.Equal(t, injectedErr, err)
}

func TestWithErrorInjectionRecorded(t *testing.T) {
	rec := recorder.New()
	inj := inject.NewInjector()
	c, _ := newTestCache(WithErrorInjection(inj), WithRecorder(rec))
	ctx := context.Background()

	injectedErr := fmt.Errorf("boom")
	inj.Set("cache", "Set", injectedErr, inject.Always{})

	// CreateCache should work since injection is only on Set.
	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "inj-cache"})
	require.NoError(t, err)

	// Set should fail due to injection.
	err = c.Set(ctx, "inj-cache", "k", []byte("v"), 0)
	require.Error(t, err)

	// Verify the error was recorded.
	setCalls := rec.CallsFor("cache", "Set")
	assert.Equal(t, 1, len(setCalls))
	assert.NotNil(t, setCalls[0].Error, "expected recorded Set call to have an error")
}

func TestWithErrorInjectionRemoved(t *testing.T) {
	inj := inject.NewInjector()
	c, _ := newTestCache(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("fail")
	inj.Set("cache", "CreateCache", injectedErr, inject.Always{})

	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "test"})
	require.Error(t, err)

	// Remove the injection rule.
	inj.Remove("cache", "CreateCache")

	_, err = c.CreateCache(ctx, driver.CacheConfig{Name: "test"})
	require.NoError(t, err)
}

func TestWithLatency(t *testing.T) {
	latency := 1 * time.Millisecond
	c, _ := newTestCache(WithLatency(latency))
	ctx := context.Background()

	// Just verify it does not break normal operation.
	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "lat-cache"})
	require.NoError(t, err)

	err = c.Set(ctx, "lat-cache", "k", []byte("v"), 0)
	require.NoError(t, err)

	item, err := c.Get(ctx, "lat-cache", "k")
	require.NoError(t, err)
	assert.Equal(t, "k", item.Key)
}

func TestAllOptionsComposed(t *testing.T) {
	rec := recorder.New()
	mc := metrics.NewCollector()
	inj := inject.NewInjector()
	latency := 1 * time.Millisecond

	c, _ := newTestCache(
		WithRecorder(rec),
		WithMetrics(mc),
		WithErrorInjection(inj),
		WithLatency(latency),
	)
	ctx := context.Background()

	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "all-opts"})
	require.NoError(t, err)

	err = c.Set(ctx, "all-opts", "k", []byte("v"), 0)
	require.NoError(t, err)

	// Verify recorder captured calls.
	assert.Equal(t, 2, rec.CallCount())

	// Verify metrics captured calls.
	q := metrics.NewQuery(mc)
	assert.Equal(t, 2, q.ByName("calls_total").Count())
}

func TestPortableDeleteCacheError(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	err := c.Delete(ctx, "no-cache", "k")
	require.Error(t, err)
}

func TestPortableKeysError(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	_, err := c.Keys(ctx, "no-cache", "*")
	require.Error(t, err)
}

func TestPortableFlushAllError(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	err := c.FlushAll(ctx, "no-cache")
	require.Error(t, err)
}

func TestPortableSetError(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	err := c.Set(ctx, "no-cache", "k", []byte("v"), 0)
	require.Error(t, err)
}

func TestPortableGetError(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	_, err := c.Get(ctx, "no-cache", "k")
	require.Error(t, err)
}
