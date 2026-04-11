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

func TestExpirePortable(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	setupCacheWithItem(t, c)

	t.Run("success", func(t *testing.T) {
		err := c.Expire(ctx, "test-cache", "key1", 10*time.Second)
		require.NoError(t, err)
	})

	t.Run("nonexistent cache", func(t *testing.T) {
		err := c.Expire(ctx, "no-cache", "key1", 10*time.Second)
		require.Error(t, err)
	})

	t.Run("nonexistent key", func(t *testing.T) {
		err := c.Expire(ctx, "test-cache", "missing-key", 10*time.Second)
		require.Error(t, err)
	})
}

func TestGetTTLPortable(t *testing.T) {
	c, fc := newTestCache()
	ctx := context.Background()

	setupCacheWithItem(t, c)

	t.Run("no ttl returns negative one", func(t *testing.T) {
		ttl, err := c.GetTTL(ctx, "test-cache", "key1")
		require.NoError(t, err)
		assert.Equal(t, time.Duration(-1), ttl)
	})

	t.Run("with ttl returns positive duration", func(t *testing.T) {
		err := c.Expire(ctx, "test-cache", "key1", 30*time.Second)
		require.NoError(t, err)

		ttl, err := c.GetTTL(ctx, "test-cache", "key1")
		require.NoError(t, err)
		assert.True(t, ttl > 0, "expected positive TTL, got %v", ttl)
	})

	t.Run("expired key returns error", func(t *testing.T) {
		err := c.Set(ctx, "test-cache", "expiring", []byte("val"), 5*time.Second)
		require.NoError(t, err)

		fc.Advance(10 * time.Second)

		_, err = c.GetTTL(ctx, "test-cache", "expiring")
		require.Error(t, err)
	})

	t.Run("nonexistent cache", func(t *testing.T) {
		_, err := c.GetTTL(ctx, "no-cache", "key1")
		require.Error(t, err)
	})

	t.Run("nonexistent key", func(t *testing.T) {
		_, err := c.GetTTL(ctx, "test-cache", "missing-key")
		require.Error(t, err)
	})
}

func TestPersistPortable(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	setupCacheWithItem(t, c)

	t.Run("success", func(t *testing.T) {
		err := c.Expire(ctx, "test-cache", "key1", 30*time.Second)
		require.NoError(t, err)

		ttl, err := c.GetTTL(ctx, "test-cache", "key1")
		require.NoError(t, err)
		assert.True(t, ttl > 0, "expected positive TTL before persist")

		err = c.Persist(ctx, "test-cache", "key1")
		require.NoError(t, err)

		ttl, err = c.GetTTL(ctx, "test-cache", "key1")
		require.NoError(t, err)
		assert.Equal(t, time.Duration(-1), ttl, "expected TTL -1 after persist")
	})

	t.Run("nonexistent cache", func(t *testing.T) {
		err := c.Persist(ctx, "no-cache", "key1")
		require.Error(t, err)
	})

	t.Run("nonexistent key", func(t *testing.T) {
		err := c.Persist(ctx, "test-cache", "missing-key")
		require.Error(t, err)
	})
}

func TestIncrPortable(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "incr-cache"})
	require.NoError(t, err)

	t.Run("increment existing numeric key", func(t *testing.T) {
		err := c.Set(ctx, "incr-cache", "counter", []byte("10"), 0)
		require.NoError(t, err)

		val, incrErr := c.Incr(ctx, "incr-cache", "counter")
		require.NoError(t, incrErr)
		assert.Equal(t, int64(11), val)
	})

	t.Run("increment nonexistent key initializes to one", func(t *testing.T) {
		val, incrErr := c.Incr(ctx, "incr-cache", "new-counter")
		require.NoError(t, incrErr)
		assert.Equal(t, int64(1), val)
	})

	t.Run("nonexistent cache", func(t *testing.T) {
		_, incrErr := c.Incr(ctx, "no-cache", "counter")
		require.Error(t, incrErr)
	})

	t.Run("non-numeric value", func(t *testing.T) {
		err := c.Set(ctx, "incr-cache", "text-key", []byte("hello"), 0)
		require.NoError(t, err)

		_, incrErr := c.Incr(ctx, "incr-cache", "text-key")
		require.Error(t, incrErr)
	})
}

func TestIncrByPortable(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "incrby-cache"})
	require.NoError(t, err)

	t.Run("increment by delta", func(t *testing.T) {
		err := c.Set(ctx, "incrby-cache", "counter", []byte("10"), 0)
		require.NoError(t, err)

		val, incrErr := c.IncrBy(ctx, "incrby-cache", "counter", 5)
		require.NoError(t, incrErr)
		assert.Equal(t, int64(15), val)
	})

	t.Run("increment nonexistent key by delta", func(t *testing.T) {
		val, incrErr := c.IncrBy(ctx, "incrby-cache", "new-counter", 5)
		require.NoError(t, incrErr)
		assert.Equal(t, int64(5), val)
	})

	t.Run("nonexistent cache", func(t *testing.T) {
		_, incrErr := c.IncrBy(ctx, "no-cache", "counter", 5)
		require.Error(t, incrErr)
	})

	t.Run("non-numeric value", func(t *testing.T) {
		err := c.Set(ctx, "incrby-cache", "text-key", []byte("hello"), 0)
		require.NoError(t, err)

		_, incrErr := c.IncrBy(ctx, "incrby-cache", "text-key", 5)
		require.Error(t, incrErr)
	})
}

func TestDecrPortable(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "decr-cache"})
	require.NoError(t, err)

	t.Run("decrement existing numeric key", func(t *testing.T) {
		err := c.Set(ctx, "decr-cache", "counter", []byte("10"), 0)
		require.NoError(t, err)

		val, decrErr := c.Decr(ctx, "decr-cache", "counter")
		require.NoError(t, decrErr)
		assert.Equal(t, int64(9), val)
	})

	t.Run("decrement nonexistent key initializes to negative one", func(t *testing.T) {
		val, decrErr := c.Decr(ctx, "decr-cache", "new-counter")
		require.NoError(t, decrErr)
		assert.Equal(t, int64(-1), val)
	})

	t.Run("nonexistent cache", func(t *testing.T) {
		_, decrErr := c.Decr(ctx, "no-cache", "counter")
		require.Error(t, decrErr)
	})

	t.Run("non-numeric value", func(t *testing.T) {
		err := c.Set(ctx, "decr-cache", "text-key", []byte("hello"), 0)
		require.NoError(t, err)

		_, decrErr := c.Decr(ctx, "decr-cache", "text-key")
		require.Error(t, decrErr)
	})
}

func TestDecrByPortable(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "decrby-cache"})
	require.NoError(t, err)

	t.Run("decrement by delta", func(t *testing.T) {
		err := c.Set(ctx, "decrby-cache", "counter", []byte("10"), 0)
		require.NoError(t, err)

		val, decrErr := c.DecrBy(ctx, "decrby-cache", "counter", 3)
		require.NoError(t, decrErr)
		assert.Equal(t, int64(7), val)
	})

	t.Run("decrement nonexistent key by delta", func(t *testing.T) {
		val, decrErr := c.DecrBy(ctx, "decrby-cache", "new-counter", 3)
		require.NoError(t, decrErr)
		assert.Equal(t, int64(-3), val)
	})

	t.Run("nonexistent cache", func(t *testing.T) {
		_, decrErr := c.DecrBy(ctx, "no-cache", "counter", 3)
		require.Error(t, decrErr)
	})

	t.Run("non-numeric value", func(t *testing.T) {
		err := c.Set(ctx, "decrby-cache", "text-key", []byte("hello"), 0)
		require.NoError(t, err)

		_, decrErr := c.DecrBy(ctx, "decrby-cache", "text-key", 3)
		require.Error(t, decrErr)
	})
}

func TestExpireRecorderAndMetrics(t *testing.T) {
	rec := recorder.New()
	mc := metrics.NewCollector()
	c, _ := newTestCache(WithRecorder(rec), WithMetrics(mc))
	ctx := context.Background()

	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "rm-cache"})
	require.NoError(t, err)

	err = c.Set(ctx, "rm-cache", "k", []byte("v"), 0)
	require.NoError(t, err)

	err = c.Expire(ctx, "rm-cache", "k", 10*time.Second)
	require.NoError(t, err)

	_, err = c.GetTTL(ctx, "rm-cache", "k")
	require.NoError(t, err)

	err = c.Persist(ctx, "rm-cache", "k")
	require.NoError(t, err)

	expireCalls := rec.CallCountFor("cache", "Expire")
	assert.Equal(t, 1, expireCalls)

	getTTLCalls := rec.CallCountFor("cache", "GetTTL")
	assert.Equal(t, 1, getTTLCalls)

	persistCalls := rec.CallCountFor("cache", "Persist")
	assert.Equal(t, 1, persistCalls)

	q := metrics.NewQuery(mc)
	callsCount := q.ByName("calls_total").Count()
	assert.GreaterOrEqual(t, callsCount, 5)
}

func TestCounterRecorderAndMetrics(t *testing.T) {
	rec := recorder.New()
	mc := metrics.NewCollector()
	c, _ := newTestCache(WithRecorder(rec), WithMetrics(mc))
	ctx := context.Background()

	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "cnt-cache"})
	require.NoError(t, err)

	err = c.Set(ctx, "cnt-cache", "k", []byte("0"), 0)
	require.NoError(t, err)

	_, err = c.Incr(ctx, "cnt-cache", "k")
	require.NoError(t, err)

	_, err = c.IncrBy(ctx, "cnt-cache", "k", 5)
	require.NoError(t, err)

	_, err = c.Decr(ctx, "cnt-cache", "k")
	require.NoError(t, err)

	_, err = c.DecrBy(ctx, "cnt-cache", "k", 3)
	require.NoError(t, err)

	incrCalls := rec.CallCountFor("cache", "Incr")
	assert.Equal(t, 1, incrCalls)

	incrByCalls := rec.CallCountFor("cache", "IncrBy")
	assert.Equal(t, 1, incrByCalls)

	decrCalls := rec.CallCountFor("cache", "Decr")
	assert.Equal(t, 1, decrCalls)

	decrByCalls := rec.CallCountFor("cache", "DecrBy")
	assert.Equal(t, 1, decrByCalls)

	q := metrics.NewQuery(mc)
	callsCount := q.ByName("calls_total").Count()
	assert.GreaterOrEqual(t, callsCount, 6)
}

func TestCounterErrorInjection(t *testing.T) {
	inj := inject.NewInjector()
	c, _ := newTestCache(WithErrorInjection(inj))
	ctx := context.Background()

	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "ei-cache"})
	require.NoError(t, err)

	err = c.Set(ctx, "ei-cache", "k", []byte("0"), 0)
	require.NoError(t, err)

	injectedErr := fmt.Errorf("injected")

	t.Run("Expire injection", func(t *testing.T) {
		inj.Set("cache", "Expire", injectedErr, inject.Always{})
		err := c.Expire(ctx, "ei-cache", "k", 10*time.Second)
		require.Error(t, err)
		assert.Equal(t, injectedErr, err)
		inj.Remove("cache", "Expire")
	})

	t.Run("GetTTL injection", func(t *testing.T) {
		inj.Set("cache", "GetTTL", injectedErr, inject.Always{})
		_, err := c.GetTTL(ctx, "ei-cache", "k")
		require.Error(t, err)
		assert.Equal(t, injectedErr, err)
		inj.Remove("cache", "GetTTL")
	})

	t.Run("Persist injection", func(t *testing.T) {
		inj.Set("cache", "Persist", injectedErr, inject.Always{})
		err := c.Persist(ctx, "ei-cache", "k")
		require.Error(t, err)
		assert.Equal(t, injectedErr, err)
		inj.Remove("cache", "Persist")
	})

	t.Run("Incr injection", func(t *testing.T) {
		inj.Set("cache", "Incr", injectedErr, inject.Always{})
		_, err := c.Incr(ctx, "ei-cache", "k")
		require.Error(t, err)
		assert.Equal(t, injectedErr, err)
		inj.Remove("cache", "Incr")
	})

	t.Run("IncrBy injection", func(t *testing.T) {
		inj.Set("cache", "IncrBy", injectedErr, inject.Always{})
		_, err := c.IncrBy(ctx, "ei-cache", "k", 5)
		require.Error(t, err)
		assert.Equal(t, injectedErr, err)
		inj.Remove("cache", "IncrBy")
	})

	t.Run("Decr injection", func(t *testing.T) {
		inj.Set("cache", "Decr", injectedErr, inject.Always{})
		_, err := c.Decr(ctx, "ei-cache", "k")
		require.Error(t, err)
		assert.Equal(t, injectedErr, err)
		inj.Remove("cache", "Decr")
	})

	t.Run("DecrBy injection", func(t *testing.T) {
		inj.Set("cache", "DecrBy", injectedErr, inject.Always{})
		_, err := c.DecrBy(ctx, "ei-cache", "k", 3)
		require.Error(t, err)
		assert.Equal(t, injectedErr, err)
		inj.Remove("cache", "DecrBy")
	})
}
