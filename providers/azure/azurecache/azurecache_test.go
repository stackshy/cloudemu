package azurecache

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/cache/driver"
	"github.com/stackshy/cloudemu/config"
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

func newTestMock() (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("eastus"))

	return New(opts), fc
}

func createTestCache(t *testing.T, m *Mock, name string) {
	t.Helper()

	_, err := m.CreateCache(context.Background(), driver.CacheConfig{Name: name})
	if err != nil {
		t.Fatalf("failed to create test cache: %v", err)
	}
}

func TestCreateCache(t *testing.T) {
	tests := []struct {
		name      string
		cfg       driver.CacheConfig
		setup     func(*Mock)
		expectErr bool
	}{
		{name: "success with defaults", cfg: driver.CacheConfig{Name: "my-cache"}},
		{
			name: "success with custom engine and node type",
			cfg:  driver.CacheConfig{Name: "custom", Engine: "memcached", NodeType: "Premium_P1"},
		},
		{name: "empty name", cfg: driver.CacheConfig{}, expectErr: true},
		{
			name: "duplicate cache",
			cfg:  driver.CacheConfig{Name: "dup"},
			setup: func(m *Mock) {
				createTestCache(&testing.T{}, m, "dup")
			},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestMock()

			if tc.setup != nil {
				tc.setup(m)
			}

			info, err := m.CreateCache(context.Background(), tc.cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertEqual(t, tc.cfg.Name, info.Name)
			assertEqual(t, "Running", info.Status)
			assertNotEmpty(t, info.Endpoint)
			assertNotEmpty(t, info.CreatedAt)
		})
	}
}

func TestCreateCacheDefaults(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	info, err := m.CreateCache(ctx, driver.CacheConfig{Name: "default-cache"})
	requireNoError(t, err)

	assertEqual(t, "redis", info.Engine)
	assertEqual(t, "Standard_C1", info.NodeType)
}

func TestCreateCacheCustomValues(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	info, err := m.CreateCache(ctx, driver.CacheConfig{
		Name:     "custom-cache",
		Engine:   "memcached",
		NodeType: "Premium_P1",
	})
	requireNoError(t, err)

	assertEqual(t, "memcached", info.Engine)
	assertEqual(t, "Premium_P1", info.NodeType)
}

func TestDeleteCache(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestCache(t, m, "to-delete")

	t.Run("success", func(t *testing.T) {
		err := m.DeleteCache(ctx, "to-delete")
		requireNoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.DeleteCache(ctx, "nonexistent")
		assertError(t, err, true)
	})
}

func TestGetCache(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestCache(t, m, "my-cache")

	t.Run("success", func(t *testing.T) {
		info, err := m.GetCache(ctx, "my-cache")
		requireNoError(t, err)
		assertEqual(t, "my-cache", info.Name)
		assertEqual(t, "Running", info.Status)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetCache(ctx, "nonexistent")
		assertError(t, err, true)
	})
}

func TestListCaches(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	t.Run("empty list", func(t *testing.T) {
		caches, err := m.ListCaches(ctx)
		requireNoError(t, err)
		assertEqual(t, 0, len(caches))
	})

	createTestCache(t, m, "cache-a")
	createTestCache(t, m, "cache-b")

	t.Run("two caches", func(t *testing.T) {
		caches, err := m.ListCaches(ctx)
		requireNoError(t, err)
		assertEqual(t, 2, len(caches))
	})
}

func TestSet(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestCache(t, m, "my-cache")

	t.Run("success", func(t *testing.T) {
		err := m.Set(ctx, "my-cache", "key1", []byte("value1"), 0)
		requireNoError(t, err)
	})

	t.Run("with TTL", func(t *testing.T) {
		ttl := 5 * time.Minute
		err := m.Set(ctx, "my-cache", "key-ttl", []byte("val"), ttl)
		requireNoError(t, err)
	})

	t.Run("nonexistent cache", func(t *testing.T) {
		err := m.Set(ctx, "no-cache", "key1", []byte("val"), 0)
		assertError(t, err, true)
	})
}

func TestGet(t *testing.T) {
	m, fc := newTestMock()
	ctx := context.Background()

	createTestCache(t, m, "my-cache")

	err := m.Set(ctx, "my-cache", "key1", []byte("value1"), 0)
	requireNoError(t, err)

	t.Run("success", func(t *testing.T) {
		item, getErr := m.Get(ctx, "my-cache", "key1")
		requireNoError(t, getErr)
		assertEqual(t, "key1", item.Key)
		assertEqual(t, string([]byte("value1")), string(item.Value))
	})

	t.Run("nonexistent cache", func(t *testing.T) {
		_, getErr := m.Get(ctx, "no-cache", "key1")
		assertError(t, getErr, true)
	})

	t.Run("nonexistent key", func(t *testing.T) {
		_, getErr := m.Get(ctx, "my-cache", "missing")
		assertError(t, getErr, true)
	})

	t.Run("expired key", func(t *testing.T) {
		ttl := 10 * time.Second
		setErr := m.Set(ctx, "my-cache", "expiring", []byte("temp"), ttl)
		requireNoError(t, setErr)

		fc.Advance(20 * time.Second)

		_, getErr := m.Get(ctx, "my-cache", "expiring")
		assertError(t, getErr, true)
	})

	t.Run("not yet expired key", func(t *testing.T) {
		ttl := 30 * time.Minute
		setErr := m.Set(ctx, "my-cache", "long-lived", []byte("still-here"), ttl)
		requireNoError(t, setErr)

		fc.Advance(10 * time.Minute)

		item, getErr := m.Get(ctx, "my-cache", "long-lived")
		requireNoError(t, getErr)
		assertEqual(t, "long-lived", item.Key)

		if item.TTL <= 0 {
			t.Error("expected positive TTL for non-expired key")
		}
	})
}

func TestDelete(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestCache(t, m, "my-cache")

	err := m.Set(ctx, "my-cache", "key1", []byte("val"), 0)
	requireNoError(t, err)

	t.Run("success", func(t *testing.T) {
		delErr := m.Delete(ctx, "my-cache", "key1")
		requireNoError(t, delErr)
	})

	t.Run("nonexistent key", func(t *testing.T) {
		delErr := m.Delete(ctx, "my-cache", "missing")
		assertError(t, delErr, true)
	})

	t.Run("nonexistent cache", func(t *testing.T) {
		delErr := m.Delete(ctx, "no-cache", "key1")
		assertError(t, delErr, true)
	})
}

func TestKeys(t *testing.T) {
	m, fc := newTestMock()
	ctx := context.Background()

	createTestCache(t, m, "my-cache")

	err := m.Set(ctx, "my-cache", "user:1", []byte("a"), 0)
	requireNoError(t, err)

	err = m.Set(ctx, "my-cache", "user:2", []byte("b"), 0)
	requireNoError(t, err)

	err = m.Set(ctx, "my-cache", "session:abc", []byte("c"), 0)
	requireNoError(t, err)

	t.Run("wildcard all", func(t *testing.T) {
		keys, keysErr := m.Keys(ctx, "my-cache", "*")
		requireNoError(t, keysErr)
		assertEqual(t, 3, len(keys))
	})

	t.Run("prefix match", func(t *testing.T) {
		keys, keysErr := m.Keys(ctx, "my-cache", "user:*")
		requireNoError(t, keysErr)
		assertEqual(t, 2, len(keys))
	})

	t.Run("suffix match", func(t *testing.T) {
		keys, keysErr := m.Keys(ctx, "my-cache", "*abc")
		requireNoError(t, keysErr)
		assertEqual(t, 1, len(keys))
	})

	t.Run("exact match", func(t *testing.T) {
		keys, keysErr := m.Keys(ctx, "my-cache", "user:1")
		requireNoError(t, keysErr)
		assertEqual(t, 1, len(keys))
	})

	t.Run("empty pattern returns all", func(t *testing.T) {
		keys, keysErr := m.Keys(ctx, "my-cache", "")
		requireNoError(t, keysErr)
		assertEqual(t, 3, len(keys))
	})

	t.Run("no match", func(t *testing.T) {
		keys, keysErr := m.Keys(ctx, "my-cache", "orders:*")
		requireNoError(t, keysErr)
		assertEqual(t, 0, len(keys))
	})

	t.Run("nonexistent cache", func(t *testing.T) {
		_, keysErr := m.Keys(ctx, "no-cache", "*")
		assertError(t, keysErr, true)
	})

	t.Run("expired keys filtered out", func(t *testing.T) {
		ttl := 5 * time.Second
		setErr := m.Set(ctx, "my-cache", "temp:1", []byte("x"), ttl)
		requireNoError(t, setErr)

		fc.Advance(10 * time.Second)

		keys, keysErr := m.Keys(ctx, "my-cache", "temp:*")
		requireNoError(t, keysErr)
		assertEqual(t, 0, len(keys))
	})
}

func TestFlushAll(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestCache(t, m, "my-cache")

	err := m.Set(ctx, "my-cache", "k1", []byte("v1"), 0)
	requireNoError(t, err)

	err = m.Set(ctx, "my-cache", "k2", []byte("v2"), 0)
	requireNoError(t, err)

	t.Run("success", func(t *testing.T) {
		flushErr := m.FlushAll(ctx, "my-cache")
		requireNoError(t, flushErr)

		keys, keysErr := m.Keys(ctx, "my-cache", "*")
		requireNoError(t, keysErr)
		assertEqual(t, 0, len(keys))
	})

	t.Run("nonexistent cache", func(t *testing.T) {
		flushErr := m.FlushAll(ctx, "no-cache")
		assertError(t, flushErr, true)
	})
}

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		key     string
		want    bool
	}{
		{name: "empty pattern matches all", pattern: "", key: "anything", want: true},
		{name: "star matches all", pattern: "*", key: "anything", want: true},
		{name: "prefix star match", pattern: "user:*", key: "user:123", want: true},
		{name: "prefix star no match", pattern: "user:*", key: "session:1", want: false},
		{name: "suffix star match", pattern: "*:end", key: "key:end", want: true},
		{name: "suffix star no match", pattern: "*:end", key: "key:start", want: false},
		{name: "exact match", pattern: "exact", key: "exact", want: true},
		{name: "exact no match", pattern: "exact", key: "other", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := matchPattern(tc.pattern, tc.key)
			assertEqual(t, tc.want, got)
		})
	}
}
