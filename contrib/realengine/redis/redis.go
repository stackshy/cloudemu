// Package redis provides an opt-in real Redis cache engine (backed by
// in-process miniredis servers) that backs CloudEmu's cache instances, so
// clients can run real Redis commands against the emulator instead of a
// synthetic endpoint. Wire it in with config.WithCacheEngine(redis.New()).
package redis

import (
	"context"
	"strconv"
	"sync"

	"github.com/alicebob/miniredis/v2"
	"github.com/stackshy/cloudemu/v2/config"
)

// Redis is a config.CacheEngine backed by real in-process Redis servers
// (miniredis), one per provisioned cache. Clients connect over the real Redis
// protocol and run real commands — no Docker. Safe for concurrent use.
type Redis struct {
	mu      sync.Mutex
	servers map[string]*miniredis.Miniredis // cacheID -> server
}

// New returns a Redis cache engine. Each provisioned cache gets its own
// isolated in-process Redis server.
func New() *Redis {
	return &Redis{servers: map[string]*miniredis.Miniredis{}}
}

// Provision starts a real Redis server for the cache and returns its address.
// It is idempotent per cache ID.
func (r *Redis) Provision(_ context.Context, req config.CacheProvisionRequest) (config.ProvisionResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if srv, ok := r.servers[req.CacheID]; ok {
		return addr(srv), nil
	}

	srv, err := miniredis.Run()
	if err != nil {
		return config.ProvisionResult{}, err
	}

	r.servers[req.CacheID] = srv

	return addr(srv), nil
}

// Deprovision stops the Redis server backing the cache. No-op if unknown.
func (r *Redis) Deprovision(_ context.Context, cacheID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	srv, ok := r.servers[cacheID]
	if !ok {
		return nil
	}

	srv.Close()
	delete(r.servers, cacheID)

	return nil
}

// Close stops every running Redis server. Safe to call more than once.
func (r *Redis) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for id, srv := range r.servers {
		srv.Close()
		delete(r.servers, id)
	}

	return nil
}

func addr(srv *miniredis.Miniredis) config.ProvisionResult {
	port, _ := strconv.Atoi(srv.Port())

	return config.ProvisionResult{Host: srv.Host(), Port: port}
}

// staticCacheEngineCheck asserts Redis satisfies the config.CacheEngine contract.
var _ config.CacheEngine = (*Redis)(nil)
