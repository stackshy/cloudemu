// Package cacheengine wires an optional real cache server into a cache
// provider's instance lifecycle. It is shared by every cache provider (AWS
// ElastiCache, Azure Cache for Redis, GCP Memorystore) so the provision hook
// stays identical across clouds and cannot drift.
package cacheengine

import (
	"context"
	"fmt"
	"strings"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
)

const engineRedis = "redis"

// IsRedisFamily reports whether a real Redis server can back this engine. The
// no-Docker miniredis backing speaks the Redis (RESP) protocol. Matching is
// case-insensitive — providers spell the engine "redis"/"Redis".
func IsRedisFamily(engine string) bool {
	return strings.EqualFold(engine, engineRedis)
}

// Provision backs the cache with the engine when one is configured and the
// engine is supported, overriding the synthetic endpoint with the real
// host:port a client connects to. No-op otherwise.
func Provision(ctx context.Context, engine config.CacheEngine, info *cachedriver.CacheInfo) error {
	if engine == nil || !IsRedisFamily(info.Engine) {
		return nil
	}

	res, err := engine.Provision(ctx, config.CacheProvisionRequest{
		CacheID: info.Name,
		Engine:  info.Engine,
	})
	if err != nil {
		return cerrors.Newf(cerrors.Internal, "provision cache engine: %v", err)
	}

	info.Endpoint = fmt.Sprintf("%s:%d", res.Host, res.Port)

	return nil
}

// Deprovision tears down the real cache server backing the instance, if any.
func Deprovision(ctx context.Context, engine config.CacheEngine, info *cachedriver.CacheInfo) error {
	if engine == nil || !IsRedisFamily(info.Engine) {
		return nil
	}

	if err := engine.Deprovision(ctx, info.Name); err != nil {
		return cerrors.Newf(cerrors.Internal, "deprovision cache engine: %v", err)
	}

	return nil
}
