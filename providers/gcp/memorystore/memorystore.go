// Package memorystore provides an in-memory mock implementation of GCP Memorystore.
package memorystore

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"time"

	"github.com/stackshy/cloudemu/cache/driver"
	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
)

const defaultRedisPort = 6379

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

// Mock is an in-memory mock implementation of GCP Memorystore.
type Mock struct {
	caches     *memstore.Store[*cacheData]
	opts       *config.Options
	monitoring mondriver.Monitoring
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

//nolint:unparam // value kept as parameter for API consistency with other service emitMetric helpers.
func (m *Mock) emitMetric(ctx context.Context, metricName string, value float64, dims map[string]string) {
	if m.monitoring == nil {
		return
	}

	_ = m.monitoring.PutMetricData(ctx, []mondriver.MetricDatum{
		{
			Namespace:  "redis.googleapis.com",
			MetricName: metricName,
			Value:      value,
			Unit:       "None",
			Dimensions: dims,
			Timestamp:  m.opts.Clock.Now(),
		},
	})
}

// New creates a new Memorystore mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		caches: memstore.New[*cacheData](),
		opts:   opts,
	}
}

// CreateCache creates a new Memorystore instance.
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
		nodeType = "M1"
	}

	selfLink := idgen.GCPID(m.opts.ProjectID, "instances", cfg.Name)
	endpoint := fmt.Sprintf("%s.redis.%s.gcp.cloud:%d", cfg.Name, m.opts.Region, defaultRedisPort)

	tags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	info := driver.CacheInfo{
		Name:      selfLink,
		NodeType:  nodeType,
		Engine:    engine,
		Status:    "READY",
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

// DeleteCache deletes a Memorystore instance by name.
func (m *Mock) DeleteCache(_ context.Context, name string) error {
	if !m.caches.Delete(name) {
		return errors.Newf(errors.NotFound, "cache %q not found", name)
	}

	return nil
}

// GetCache retrieves information about a Memorystore instance.
func (m *Mock) GetCache(_ context.Context, name string) (*driver.CacheInfo, error) {
	cd, ok := m.caches.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "cache %q not found", name)
	}

	result := cd.info

	return &result, nil
}

// ListCaches lists all Memorystore instances.
func (m *Mock) ListCaches(_ context.Context) ([]driver.CacheInfo, error) {
	all := m.caches.All()

	caches := make([]driver.CacheInfo, 0, len(all))
	for _, cd := range all {
		caches = append(caches, cd.info)
	}

	return caches, nil
}

// Set stores a value in the cache with an optional TTL.
func (m *Mock) Set(ctx context.Context, cacheName, key string, value []byte, ttl time.Duration) error {
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

	dims := map[string]string{"instance_id": cacheName}
	m.emitMetric(ctx, "commands/set", 1, dims)
	m.emitMetric(ctx, "commands/total", 1, dims)

	return nil
}

// Get retrieves a value from the cache.
func (m *Mock) Get(ctx context.Context, cacheName, key string) (*driver.Item, error) {
	cd, ok := m.caches.Get(cacheName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "cache %q not found", cacheName)
	}

	item, ok := cd.items.Get(key)
	if !ok {
		dims := map[string]string{"instance_id": cacheName}
		m.emitMetric(ctx, "stats/cache_miss_count", 1, dims)
		m.emitMetric(ctx, "commands/get", 1, dims)
		m.emitMetric(ctx, "commands/total", 1, dims)

		return nil, errors.Newf(errors.NotFound, "key %q not found in cache %q", key, cacheName)
	}

	now := m.opts.Clock.Now()
	if item.HasTTL && now.After(item.ExpiresAt) {
		cd.items.Delete(key)

		dims := map[string]string{"instance_id": cacheName}
		m.emitMetric(ctx, "stats/cache_miss_count", 1, dims)
		m.emitMetric(ctx, "commands/get", 1, dims)
		m.emitMetric(ctx, "commands/total", 1, dims)

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

	dims := map[string]string{"instance_id": cacheName}
	m.emitMetric(ctx, "stats/cache_hit_count", 1, dims)
	m.emitMetric(ctx, "commands/get", 1, dims)
	m.emitMetric(ctx, "commands/total", 1, dims)

	return result, nil
}

// Delete removes a value from the cache.
func (m *Mock) Delete(ctx context.Context, cacheName, key string) error {
	cd, ok := m.caches.Get(cacheName)
	if !ok {
		return errors.Newf(errors.NotFound, "cache %q not found", cacheName)
	}

	if !cd.items.Delete(key) {
		return errors.Newf(errors.NotFound, "key %q not found in cache %q", key, cacheName)
	}

	m.emitMetric(ctx, "commands/total", 1, map[string]string{"instance_id": cacheName})

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

// Expire sets a TTL on an existing key.
func (m *Mock) Expire(_ context.Context, cacheName, key string, ttl time.Duration) error {
	cd, ok := m.caches.Get(cacheName)
	if !ok {
		return errors.Newf(errors.NotFound, "cache %q not found", cacheName)
	}

	item, ok := cd.items.Get(key)
	if !ok || (item.HasTTL && m.opts.Clock.Now().After(item.ExpiresAt)) {
		return errors.Newf(errors.NotFound, "key %q not found in cache %q", key, cacheName)
	}

	item.HasTTL = true
	item.ExpiresAt = m.opts.Clock.Now().Add(ttl)
	cd.items.Set(key, item)

	return nil
}

// GetTTL returns the remaining TTL for a key. Returns -1 if the key has no TTL.
func (m *Mock) GetTTL(_ context.Context, cacheName, key string) (time.Duration, error) {
	cd, ok := m.caches.Get(cacheName)
	if !ok {
		return 0, errors.Newf(errors.NotFound, "cache %q not found", cacheName)
	}

	item, ok := cd.items.Get(key)
	if !ok || (item.HasTTL && m.opts.Clock.Now().After(item.ExpiresAt)) {
		return 0, errors.Newf(errors.NotFound, "key %q not found in cache %q", key, cacheName)
	}

	if !item.HasTTL {
		return -1, nil
	}

	return item.ExpiresAt.Sub(m.opts.Clock.Now()), nil
}

// Persist removes the TTL from a key, making it persistent.
func (m *Mock) Persist(_ context.Context, cacheName, key string) error {
	cd, ok := m.caches.Get(cacheName)
	if !ok {
		return errors.Newf(errors.NotFound, "cache %q not found", cacheName)
	}

	item, ok := cd.items.Get(key)
	if !ok || (item.HasTTL && m.opts.Clock.Now().After(item.ExpiresAt)) {
		return errors.Newf(errors.NotFound, "key %q not found in cache %q", key, cacheName)
	}

	item.HasTTL = false
	item.ExpiresAt = time.Time{}
	cd.items.Set(key, item)

	return nil
}

// Incr atomically increments the integer value of a key by 1.
func (m *Mock) Incr(ctx context.Context, cacheName, key string) (int64, error) {
	return m.IncrBy(ctx, cacheName, key, 1)
}

// IncrBy atomically increments the integer value of a key by delta.
func (m *Mock) IncrBy(ctx context.Context, cacheName, key string, delta int64) (int64, error) {
	cd, ok := m.caches.Get(cacheName)
	if !ok {
		return 0, errors.Newf(errors.NotFound, "cache %q not found", cacheName)
	}

	newVal, err := applyDelta(cd, key, delta, m.opts.Clock.Now())
	if err != nil {
		return 0, err
	}

	m.emitMetric(ctx, "commands/total", 1, map[string]string{"instance_id": cacheName})

	return newVal, nil
}

// Decr atomically decrements the integer value of a key by 1.
func (m *Mock) Decr(ctx context.Context, cacheName, key string) (int64, error) {
	return m.DecrBy(ctx, cacheName, key, 1)
}

// DecrBy atomically decrements the integer value of a key by delta.
func (m *Mock) DecrBy(ctx context.Context, cacheName, key string, delta int64) (int64, error) {
	cd, ok := m.caches.Get(cacheName)
	if !ok {
		return 0, errors.Newf(errors.NotFound, "cache %q not found", cacheName)
	}

	newVal, err := applyDelta(cd, key, -delta, m.opts.Clock.Now())
	if err != nil {
		return 0, err
	}

	m.emitMetric(ctx, "commands/total", 1, map[string]string{"instance_id": cacheName})

	return newVal, nil
}

func applyDelta(cd *cacheData, key string, delta int64, now time.Time) (int64, error) {
	item, ok := cd.items.Get(key)

	var current int64

	if ok && (!item.HasTTL || !now.After(item.ExpiresAt)) {
		val, err := strconv.ParseInt(string(item.Value), 10, 64)
		if err != nil {
			return 0, errors.New(errors.InvalidArgument, "value is not an integer")
		}

		current = val
	}

	newVal := current + delta
	newItem := cacheItem{
		Value: []byte(strconv.FormatInt(newVal, 10)),
	}

	if ok && item.HasTTL && !now.After(item.ExpiresAt) {
		newItem.HasTTL = true
		newItem.ExpiresAt = item.ExpiresAt
	}

	cd.items.Set(key, newItem)

	return newVal, nil
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
