package memorystore

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/cache/driver"
	"github.com/stackshy/cloudemu/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMock() (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("us-central1"),
		config.WithProjectID("test-project"),
	)

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
			cfg:  driver.CacheConfig{Name: "custom", Engine: "memcached", NodeType: "M3"},
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
			if tc.expectErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			assert.NotEmpty(t, info.Name)
			assert.Equal(t, "READY", info.Status)
			assert.NotEmpty(t, info.Endpoint)
			assert.NotEmpty(t, info.CreatedAt)
		})
	}
}

func TestCreateCacheDefaults(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	info, err := m.CreateCache(ctx, driver.CacheConfig{Name: "default-cache"})
	require.NoError(t, err)

	assert.Equal(t, "redis", info.Engine)
	assert.Equal(t, "M1", info.NodeType)
}

func TestCreateCacheCustomValues(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	info, err := m.CreateCache(ctx, driver.CacheConfig{
		Name:     "custom-cache",
		Engine:   "memcached",
		NodeType: "M3",
	})
	require.NoError(t, err)

	assert.Equal(t, "memcached", info.Engine)
	assert.Equal(t, "M3", info.NodeType)
}

func TestCreateCacheSelfLink(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	info, err := m.CreateCache(ctx, driver.CacheConfig{Name: "link-test"})
	require.NoError(t, err)

	expected := "projects/test-project/instances/link-test"
	assert.Equal(t, expected, info.Name)
}

func TestDeleteCache(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestCache(t, m, "to-delete")

	t.Run("success", func(t *testing.T) {
		err := m.DeleteCache(ctx, "to-delete")
		require.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.DeleteCache(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestGetCache(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestCache(t, m, "my-cache")

	t.Run("success", func(t *testing.T) {
		info, err := m.GetCache(ctx, "my-cache")
		require.NoError(t, err)
		assert.NotEmpty(t, info.Name)
		assert.Equal(t, "READY", info.Status)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetCache(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestListCaches(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	t.Run("empty list", func(t *testing.T) {
		caches, err := m.ListCaches(ctx)
		require.NoError(t, err)
		assert.Equal(t, 0, len(caches))
	})

	createTestCache(t, m, "cache-a")
	createTestCache(t, m, "cache-b")

	t.Run("two caches", func(t *testing.T) {
		caches, err := m.ListCaches(ctx)
		require.NoError(t, err)
		assert.Equal(t, 2, len(caches))
	})
}

func TestSet(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestCache(t, m, "my-cache")

	t.Run("success", func(t *testing.T) {
		err := m.Set(ctx, "my-cache", "key1", []byte("value1"), 0)
		require.NoError(t, err)
	})

	t.Run("with TTL", func(t *testing.T) {
		ttl := 5 * time.Minute
		err := m.Set(ctx, "my-cache", "key-ttl", []byte("val"), ttl)
		require.NoError(t, err)
	})

	t.Run("nonexistent cache", func(t *testing.T) {
		err := m.Set(ctx, "no-cache", "key1", []byte("val"), 0)
		require.Error(t, err)
	})
}

func TestGet(t *testing.T) {
	m, fc := newTestMock()
	ctx := context.Background()

	createTestCache(t, m, "my-cache")

	err := m.Set(ctx, "my-cache", "key1", []byte("value1"), 0)
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		item, getErr := m.Get(ctx, "my-cache", "key1")
		require.NoError(t, getErr)
		assert.Equal(t, "key1", item.Key)
		assert.Equal(t, string([]byte("value1")), string(item.Value))
	})

	t.Run("nonexistent cache", func(t *testing.T) {
		_, getErr := m.Get(ctx, "no-cache", "key1")
		require.Error(t, getErr)
	})

	t.Run("nonexistent key", func(t *testing.T) {
		_, getErr := m.Get(ctx, "my-cache", "missing")
		require.Error(t, getErr)
	})

	t.Run("expired key", func(t *testing.T) {
		ttl := 10 * time.Second
		setErr := m.Set(ctx, "my-cache", "expiring", []byte("temp"), ttl)
		require.NoError(t, setErr)

		fc.Advance(20 * time.Second)

		_, getErr := m.Get(ctx, "my-cache", "expiring")
		require.Error(t, getErr)
	})

	t.Run("not yet expired key", func(t *testing.T) {
		ttl := 30 * time.Minute
		setErr := m.Set(ctx, "my-cache", "long-lived", []byte("still-here"), ttl)
		require.NoError(t, setErr)

		fc.Advance(10 * time.Minute)

		item, getErr := m.Get(ctx, "my-cache", "long-lived")
		require.NoError(t, getErr)
		assert.Equal(t, "long-lived", item.Key)
		assert.Greater(t, item.TTL, time.Duration(0))
	})
}

func TestDelete(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestCache(t, m, "my-cache")

	err := m.Set(ctx, "my-cache", "key1", []byte("val"), 0)
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		delErr := m.Delete(ctx, "my-cache", "key1")
		require.NoError(t, delErr)
	})

	t.Run("nonexistent key", func(t *testing.T) {
		delErr := m.Delete(ctx, "my-cache", "missing")
		require.Error(t, delErr)
	})

	t.Run("nonexistent cache", func(t *testing.T) {
		delErr := m.Delete(ctx, "no-cache", "key1")
		require.Error(t, delErr)
	})
}

func TestKeys(t *testing.T) {
	m, fc := newTestMock()
	ctx := context.Background()

	createTestCache(t, m, "my-cache")

	err := m.Set(ctx, "my-cache", "user:1", []byte("a"), 0)
	require.NoError(t, err)

	err = m.Set(ctx, "my-cache", "user:2", []byte("b"), 0)
	require.NoError(t, err)

	err = m.Set(ctx, "my-cache", "session:abc", []byte("c"), 0)
	require.NoError(t, err)

	t.Run("wildcard all", func(t *testing.T) {
		keys, keysErr := m.Keys(ctx, "my-cache", "*")
		require.NoError(t, keysErr)
		assert.Equal(t, 3, len(keys))
	})

	t.Run("prefix match", func(t *testing.T) {
		keys, keysErr := m.Keys(ctx, "my-cache", "user:*")
		require.NoError(t, keysErr)
		assert.Equal(t, 2, len(keys))
	})

	t.Run("suffix match", func(t *testing.T) {
		keys, keysErr := m.Keys(ctx, "my-cache", "*abc")
		require.NoError(t, keysErr)
		assert.Equal(t, 1, len(keys))
	})

	t.Run("exact match", func(t *testing.T) {
		keys, keysErr := m.Keys(ctx, "my-cache", "user:1")
		require.NoError(t, keysErr)
		assert.Equal(t, 1, len(keys))
	})

	t.Run("empty pattern returns all", func(t *testing.T) {
		keys, keysErr := m.Keys(ctx, "my-cache", "")
		require.NoError(t, keysErr)
		assert.Equal(t, 3, len(keys))
	})

	t.Run("no match", func(t *testing.T) {
		keys, keysErr := m.Keys(ctx, "my-cache", "orders:*")
		require.NoError(t, keysErr)
		assert.Equal(t, 0, len(keys))
	})

	t.Run("nonexistent cache", func(t *testing.T) {
		_, keysErr := m.Keys(ctx, "no-cache", "*")
		require.Error(t, keysErr)
	})

	t.Run("expired keys filtered out", func(t *testing.T) {
		ttl := 5 * time.Second
		setErr := m.Set(ctx, "my-cache", "temp:1", []byte("x"), ttl)
		require.NoError(t, setErr)

		fc.Advance(10 * time.Second)

		keys, keysErr := m.Keys(ctx, "my-cache", "temp:*")
		require.NoError(t, keysErr)
		assert.Equal(t, 0, len(keys))
	})
}

func TestFlushAll(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestCache(t, m, "my-cache")

	err := m.Set(ctx, "my-cache", "k1", []byte("v1"), 0)
	require.NoError(t, err)

	err = m.Set(ctx, "my-cache", "k2", []byte("v2"), 0)
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		flushErr := m.FlushAll(ctx, "my-cache")
		require.NoError(t, flushErr)

		keys, keysErr := m.Keys(ctx, "my-cache", "*")
		require.NoError(t, keysErr)
		assert.Equal(t, 0, len(keys))
	})

	t.Run("nonexistent cache", func(t *testing.T) {
		flushErr := m.FlushAll(ctx, "no-cache")
		require.Error(t, flushErr)
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
			assert.Equal(t, tc.want, got)
		})
	}
}
