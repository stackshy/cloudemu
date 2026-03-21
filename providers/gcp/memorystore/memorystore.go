// Package memorystore provides an in-memory mock implementation of GCP Memorystore.
package memorystore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/cache/driver"
	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
)

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
	caches *memstore.Store[*cacheData]
	opts   *config.Options
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
	endpoint := fmt.Sprintf("%s.redis.%s.gcp.cloud:6379", cfg.Name, m.opts.Region)

	info := driver.CacheInfo{
		Name:      selfLink,
		NodeType:  nodeType,
		Engine:    engine,
		Status:    "READY",
		Endpoint:  endpoint,
		CreatedAt: m.opts.Clock.Now().UTC().Format(time.RFC3339),
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
		return nil, errors.Newf(errors.NotFound, "key %q not found in cache %q", key, cacheName)
	}

	now := m.opts.Clock.Now()
	if item.HasTTL && now.After(item.ExpiresAt) {
		cd.items.Delete(key)

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

func matchPattern(pattern, key string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}

	if strings.HasSuffix(pattern, "*") && !strings.Contains(pattern[:len(pattern)-1], "*") {
		return strings.HasPrefix(key, pattern[:len(pattern)-1])
	}

	if strings.HasPrefix(pattern, "*") && !strings.Contains(pattern[1:], "*") {
		return strings.HasSuffix(key, pattern[1:])
	}

	return key == pattern
}
