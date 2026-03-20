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
)

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertError(t *testing.T, err error, expectErr bool) {
	t.Helper()

	switch {
	case expectErr && err == nil:
		t.Fatal("expected error but got nil")
	case !expectErr && err != nil:
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual(t *testing.T, expected, actual any) {
	t.Helper()

	if expected != actual {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}

func assertNotEmpty(t *testing.T, s string) {
	t.Helper()

	if s == "" {
		t.Error("expected non-empty string")
	}
}

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

	if c == nil {
		t.Fatal("expected non-nil cache")
	}

	if c.driver == nil {
		t.Fatal("expected non-nil driver")
	}
}

func TestCreateCachePortable(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		info, err := c.CreateCache(ctx, driver.CacheConfig{Name: "my-cache"})
		requireNoError(t, err)
		assertEqual(t, "my-cache", info.Name)
		assertNotEmpty(t, info.Endpoint)
	})

	t.Run("empty name error", func(t *testing.T) {
		_, err := c.CreateCache(ctx, driver.CacheConfig{})
		assertError(t, err, true)
	})
}

func TestDeleteCachePortable(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "del-cache"})
	requireNoError(t, err)

	t.Run("success", func(t *testing.T) {
		delErr := c.DeleteCache(ctx, "del-cache")
		requireNoError(t, delErr)
	})

	t.Run("not found", func(t *testing.T) {
		delErr := c.DeleteCache(ctx, "nonexistent")
		assertError(t, delErr, true)
	})
}

func TestGetCachePortable(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "get-cache"})
	requireNoError(t, err)

	t.Run("success", func(t *testing.T) {
		info, getErr := c.GetCache(ctx, "get-cache")
		requireNoError(t, getErr)
		assertEqual(t, "get-cache", info.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, getErr := c.GetCache(ctx, "nonexistent")
		assertError(t, getErr, true)
	})
}

func TestListCachesPortable(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	caches, err := c.ListCaches(ctx)
	requireNoError(t, err)
	assertEqual(t, 0, len(caches))

	_, err = c.CreateCache(ctx, driver.CacheConfig{Name: "a"})
	requireNoError(t, err)

	_, err = c.CreateCache(ctx, driver.CacheConfig{Name: "b"})
	requireNoError(t, err)

	caches, err = c.ListCaches(ctx)
	requireNoError(t, err)
	assertEqual(t, 2, len(caches))
}

func TestSetGetDeletePortable(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	setupCacheWithItem(t, c)

	t.Run("get existing key", func(t *testing.T) {
		item, err := c.Get(ctx, "test-cache", "key1")
		requireNoError(t, err)
		assertEqual(t, "key1", item.Key)
		assertEqual(t, string([]byte("value1")), string(item.Value))
	})

	t.Run("delete key", func(t *testing.T) {
		err := c.Delete(ctx, "test-cache", "key1")
		requireNoError(t, err)

		_, getErr := c.Get(ctx, "test-cache", "key1")
		assertError(t, getErr, true)
	})
}

func TestKeysPortable(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "keys-cache"})
	requireNoError(t, err)

	err = c.Set(ctx, "keys-cache", "user:1", []byte("a"), 0)
	requireNoError(t, err)

	err = c.Set(ctx, "keys-cache", "user:2", []byte("b"), 0)
	requireNoError(t, err)

	keys, err := c.Keys(ctx, "keys-cache", "user:*")
	requireNoError(t, err)
	assertEqual(t, 2, len(keys))
}

func TestFlushAllPortable(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "flush-cache"})
	requireNoError(t, err)

	err = c.Set(ctx, "flush-cache", "k1", []byte("v1"), 0)
	requireNoError(t, err)

	err = c.FlushAll(ctx, "flush-cache")
	requireNoError(t, err)

	keys, err := c.Keys(ctx, "flush-cache", "*")
	requireNoError(t, err)
	assertEqual(t, 0, len(keys))
}

func TestWithRecorder(t *testing.T) {
	rec := recorder.New()
	c, _ := newTestCache(WithRecorder(rec))
	ctx := context.Background()

	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "rec-cache"})
	requireNoError(t, err)

	err = c.Set(ctx, "rec-cache", "k", []byte("v"), 0)
	requireNoError(t, err)

	_, err = c.Get(ctx, "rec-cache", "k")
	requireNoError(t, err)

	totalCalls := rec.CallCount()
	if totalCalls < 3 {
		t.Errorf("expected at least 3 recorded calls, got %d", totalCalls)
	}

	createCalls := rec.CallCountFor("cache", "CreateCache")
	assertEqual(t, 1, createCalls)

	setCalls := rec.CallCountFor("cache", "Set")
	assertEqual(t, 1, setCalls)

	getCalls := rec.CallCountFor("cache", "Get")
	assertEqual(t, 1, getCalls)
}

func TestWithRecorderOnError(t *testing.T) {
	rec := recorder.New()
	c, _ := newTestCache(WithRecorder(rec))
	ctx := context.Background()

	// This should fail and still be recorded.
	_, _ = c.GetCache(ctx, "nonexistent")

	totalCalls := rec.CallCount()
	assertEqual(t, 1, totalCalls)

	last := rec.LastCall()
	if last == nil {
		t.Fatal("expected a recorded call")
	}

	if last.Error == nil {
		t.Error("expected recorded call to have an error")
	}
}

func TestWithMetrics(t *testing.T) {
	mc := metrics.NewCollector()
	c, _ := newTestCache(WithMetrics(mc))
	ctx := context.Background()

	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "met-cache"})
	requireNoError(t, err)

	err = c.Set(ctx, "met-cache", "k", []byte("v"), 0)
	requireNoError(t, err)

	_, err = c.Get(ctx, "met-cache", "k")
	requireNoError(t, err)

	q := metrics.NewQuery(mc)

	callsCount := q.ByName("calls_total").Count()
	if callsCount < 3 {
		t.Errorf("expected at least 3 calls_total metrics, got %d", callsCount)
	}

	durCount := q.ByName("call_duration").Count()
	if durCount < 3 {
		t.Errorf("expected at least 3 call_duration metrics, got %d", durCount)
	}
}

func TestWithMetricsOnError(t *testing.T) {
	mc := metrics.NewCollector()
	c, _ := newTestCache(WithMetrics(mc))
	ctx := context.Background()

	// This should fail.
	_, _ = c.GetCache(ctx, "nonexistent")

	q := metrics.NewQuery(mc)

	errCount := q.ByName("errors_total").Count()
	assertEqual(t, 1, errCount)
}

func TestWithErrorInjection(t *testing.T) {
	inj := inject.NewInjector()
	c, _ := newTestCache(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("injected failure")
	inj.Set("cache", "CreateCache", injectedErr, inject.Always{})

	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "fail-cache"})
	assertError(t, err, true)

	if err != injectedErr {
		t.Errorf("expected injected error, got %v", err)
	}
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
	requireNoError(t, err)

	// Set should fail due to injection.
	err = c.Set(ctx, "inj-cache", "k", []byte("v"), 0)
	assertError(t, err, true)

	// Verify the error was recorded.
	setCalls := rec.CallsFor("cache", "Set")
	assertEqual(t, 1, len(setCalls))

	if setCalls[0].Error == nil {
		t.Error("expected recorded Set call to have an error")
	}
}

func TestWithErrorInjectionRemoved(t *testing.T) {
	inj := inject.NewInjector()
	c, _ := newTestCache(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("fail")
	inj.Set("cache", "CreateCache", injectedErr, inject.Always{})

	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "test"})
	assertError(t, err, true)

	// Remove the injection rule.
	inj.Remove("cache", "CreateCache")

	_, err = c.CreateCache(ctx, driver.CacheConfig{Name: "test"})
	requireNoError(t, err)
}

func TestWithLatency(t *testing.T) {
	latency := 1 * time.Millisecond
	c, _ := newTestCache(WithLatency(latency))
	ctx := context.Background()

	// Just verify it does not break normal operation.
	_, err := c.CreateCache(ctx, driver.CacheConfig{Name: "lat-cache"})
	requireNoError(t, err)

	err = c.Set(ctx, "lat-cache", "k", []byte("v"), 0)
	requireNoError(t, err)

	item, err := c.Get(ctx, "lat-cache", "k")
	requireNoError(t, err)
	assertEqual(t, "k", item.Key)
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
	requireNoError(t, err)

	err = c.Set(ctx, "all-opts", "k", []byte("v"), 0)
	requireNoError(t, err)

	// Verify recorder captured calls.
	assertEqual(t, 2, rec.CallCount())

	// Verify metrics captured calls.
	q := metrics.NewQuery(mc)
	assertEqual(t, 2, q.ByName("calls_total").Count())
}

func TestPortableDeleteCacheError(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	err := c.Delete(ctx, "no-cache", "k")
	assertError(t, err, true)
}

func TestPortableKeysError(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	_, err := c.Keys(ctx, "no-cache", "*")
	assertError(t, err, true)
}

func TestPortableFlushAllError(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	err := c.FlushAll(ctx, "no-cache")
	assertError(t, err, true)
}

func TestPortableSetError(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	err := c.Set(ctx, "no-cache", "k", []byte("v"), 0)
	assertError(t, err, true)
}

func TestPortableGetError(t *testing.T) {
	c, _ := newTestCache()
	ctx := context.Background()

	_, err := c.Get(ctx, "no-cache", "k")
	assertError(t, err, true)
}
