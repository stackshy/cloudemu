// Package azurecache provides an in-memory mock implementation of Azure Cache for Redis.
package azurecache

import (
	"context"
	"fmt"
	"path"
	"time"

	"github.com/stackshy/cloudemu/cache/driver"
	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/memstore"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
)

const defaultRedisSSLPort = 6380

// Compile-time check that Mock implements driver.Cache.
var _ driver.Cache = (*Mock)(nil)

type cacheItem struct {
	Value     []byte
	ExpiresAt time.Time
	HasTTL    bool
}

type cacheData struct {
	info  driver.CacheInfo
	items *memstore.Store[cacheItem]
}

// Mock is an in-memory mock implementation of Azure Cache for Redis.
type Mock struct {
	caches     *memstore.Store[*cacheData]
	opts       *config.Options
	monitoring mondriver.Monitoring
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

func (m *Mock) emitMetric(cacheName string, metrics map[string]float64) {
	if m.monitoring == nil {
		return
	}

	now := m.opts.Clock.Now()
	data := make([]mondriver.MetricDatum, 0, len(metrics))

	for name, value := range metrics {
		data = append(data, mondriver.MetricDatum{
			Namespace:  "Microsoft.Cache/redis",
			MetricName: name,
			Value:      value,
			Unit:       "None",
			Dimensions: map[string]string{"cacheName": cacheName},
			Timestamp:  now,
		})
	}

	_ = m.monitoring.PutMetricData(context.Background(), data)
}

// New creates a new Azure Cache mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		caches: memstore.New[*cacheData](),
		opts:   opts,
	}
}

// CreateCache creates a new Azure Cache for Redis instance.
func (m *Mock) CreateCache(_ context.Context, cfg driver.CacheConfig) (*driver.CacheInfo, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "cache name is required")
	}

	if m.caches.Has(cfg.Name) {
		return nil, errors.Newf(errors.AlreadyExists, "cache %q already exists", cfg.Name)
	}

	engine := cfg.Engine
	if engine == "" {
		engine = "redis"
	}

	nodeType := cfg.NodeType
	if nodeType == "" {
		nodeType = "Standard_C1"
	}

	endpoint := fmt.Sprintf("%s.redis.cache.windows.net:%d", cfg.Name, defaultRedisSSLPort)

	tags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	info := driver.CacheInfo{
		Name:      cfg.Name,
		NodeType:  nodeType,
		Engine:    engine,
		Status:    "Running",
		Endpoint:  endpoint,
		CreatedAt: m.opts.Clock.Now().UTC().Format(time.RFC3339),
		Tags:      tags,
	}

	cd := &cacheData{
		info:  info,
		items: memstore.New[cacheItem](),
	}

	m.caches.Set(cfg.Name, cd)

	result := info

	return &result, nil
}

// DeleteCache deletes an Azure Cache for Redis instance by name.
func (m *Mock) DeleteCache(_ context.Context, name string) error {
	if !m.caches.Delete(name) {
		return errors.Newf(errors.NotFound, "cache %q not found", name)
	}

	return nil
}

// GetCache retrieves information about an Azure Cache for Redis instance.
func (m *Mock) GetCache(_ context.Context, name string) (*driver.CacheInfo, error) {
	cd, ok := m.caches.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "cache %q not found", name)
	}

	result := cd.info

	return &result, nil
}

// ListCaches lists all Azure Cache for Redis instances.
func (m *Mock) ListCaches(_ context.Context) ([]driver.CacheInfo, error) {
	all := m.caches.All()

	caches := make([]driver.CacheInfo, 0, len(all))
	for _, cd := range all {
		caches = append(caches, cd.info)
	}

	return caches, nil
}

// Set stores a value in the cache with an optional TTL.
func (m *Mock) Set(_ context.Context, cacheName, key string, value []byte, ttl time.Duration) error {
	cd, ok := m.caches.Get(cacheName)
	if !ok {
		return errors.Newf(errors.NotFound, "cache %q not found", cacheName)
	}

	data := make([]byte, len(value))
	copy(data, value)

	item := cacheItem{Value: data}

	if ttl > 0 {
		item.ExpiresAt = m.opts.Clock.Now().Add(ttl)
		item.HasTTL = true
	}

	cd.items.Set(key, item)

	m.emitMetric(cacheName, map[string]float64{"SetCommands": 1, "TotalCommandsProcessed": 1})

	return nil
}

// Get retrieves a value from the cache.
func (m *Mock) Get(_ context.Context, cacheName, key string) (*driver.Item, error) {
	cd, ok := m.caches.Get(cacheName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "cache %q not found", cacheName)
	}

	item, ok := cd.items.Get(key)
	if !ok {
		m.emitMetric(cacheName, map[string]float64{
			"CacheMisses": 1, "GetCommands": 1, "TotalCommandsProcessed": 1,
		})

		return nil, errors.Newf(errors.NotFound, "key %q not found in cache %q", key, cacheName)
	}

	now := m.opts.Clock.Now()
	if item.HasTTL && now.After(item.ExpiresAt) {
		cd.items.Delete(key)

		m.emitMetric(cacheName, map[string]float64{
			"CacheMisses": 1, "GetCommands": 1, "TotalCommandsProcessed": 1,
		})

		return nil, errors.Newf(errors.NotFound, "key %q not found in cache %q", key, cacheName)
	}

	data := make([]byte, len(item.Value))
	copy(data, item.Value)

	result := &driver.Item{
		Key:   key,
		Value: data,
	}

	if item.HasTTL {
		result.TTL = item.ExpiresAt.Sub(now)
		result.ExpiresAt = item.ExpiresAt
	}

	m.emitMetric(cacheName, map[string]float64{
		"CacheHits": 1, "GetCommands": 1, "TotalCommandsProcessed": 1,
	})

	return result, nil
}

// Delete removes a value from the cache.
func (m *Mock) Delete(_ context.Context, cacheName, key string) error {
	cd, ok := m.caches.Get(cacheName)
	if !ok {
		return errors.Newf(errors.NotFound, "cache %q not found", cacheName)
	}

	if !cd.items.Delete(key) {
		return errors.Newf(errors.NotFound, "key %q not found in cache %q", key, cacheName)
	}

	m.emitMetric(cacheName, map[string]float64{"TotalCommandsProcessed": 1})

	return nil
}

// Keys returns all keys matching the given pattern in the cache.
func (m *Mock) Keys(_ context.Context, cacheName, pattern string) ([]string, error) {
	cd, ok := m.caches.Get(cacheName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "cache %q not found", cacheName)
	}

	now := m.opts.Clock.Now()
	allKeys := cd.items.Keys()

	var matched []string

	for _, key := range allKeys {
		item, ok := cd.items.Get(key)
		if !ok {
			continue
		}

		if item.HasTTL && now.After(item.ExpiresAt) {
			cd.items.Delete(key)

			continue
		}

		if matchPattern(pattern, key) {
			matched = append(matched, key)
		}
	}

	if matched == nil {
		matched = []string{}
	}

	return matched, nil
}

// FlushAll removes all items from the cache.
func (m *Mock) FlushAll(_ context.Context, cacheName string) error {
	cd, ok := m.caches.Get(cacheName)
	if !ok {
		return errors.Newf(errors.NotFound, "cache %q not found", cacheName)
	}

	cd.items.Clear()

	return nil
}

// matchPattern matches a key against a glob-like pattern.
// Supports full glob syntax including middle wildcards like "user:*:session".
func matchPattern(pattern, key string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}

	matched, err := path.Match(pattern, key)
	if err != nil {
		return false
	}

	return matched
}
