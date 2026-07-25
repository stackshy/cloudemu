// Package driver defines the interface for cache service implementations.
package driver

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/services/scope"
)

// Item represents a cached item.
type Item struct {
	Key       string
	Value     []byte
	TTL       time.Duration
	ExpiresAt time.Time
}

// CacheInfo describes a cache instance.
type CacheInfo struct {
	Name      string
	NodeType  string
	Engine    string
	Status    string
	Endpoint  string
	CreatedAt string
	Tags      map[string]string
	Scope     scope.Scope
}

// CacheConfig describes a cache instance to create.
type CacheConfig struct {
	Name     string
	NodeType string
	Engine   string // "redis", "memcached"
	Tags     map[string]string

	// Scope records where the resource lives (Azure subscription/resource
	// group, GCP project). Zero for AWS and unscoped portable callers.
	Scope scope.Scope
}

// Cache is the interface that cache provider implementations must satisfy.
type Cache interface {
	CreateCache(ctx context.Context, config CacheConfig) (*CacheInfo, error)

	// UpdateCache replaces the mutable fields (node type, tags) of an
	// existing cache, mirroring ARM CreateOrUpdate-on-existing.
	UpdateCache(ctx context.Context, config CacheConfig) (*CacheInfo, error)
	DeleteCache(ctx context.Context, name string) error
	GetCache(ctx context.Context, name string) (*CacheInfo, error)
	ListCaches(ctx context.Context, filter scope.Scope) ([]CacheInfo, error)

	Set(ctx context.Context, cacheName, key string, value []byte, ttl time.Duration) error
	Get(ctx context.Context, cacheName, key string) (*Item, error)
	Delete(ctx context.Context, cacheName, key string) error
	Keys(ctx context.Context, cacheName, pattern string) ([]string, error)
	FlushAll(ctx context.Context, cacheName string) error

	// TTL management
	Expire(ctx context.Context, cacheName, key string, ttl time.Duration) error
	GetTTL(ctx context.Context, cacheName, key string) (time.Duration, error)
	Persist(ctx context.Context, cacheName, key string) error

	// Atomic counters
	Incr(ctx context.Context, cacheName, key string) (int64, error)
	IncrBy(ctx context.Context, cacheName, key string, delta int64) (int64, error)
	Decr(ctx context.Context, cacheName, key string) (int64, error)
	DecrBy(ctx context.Context, cacheName, key string, delta int64) (int64, error)
}
