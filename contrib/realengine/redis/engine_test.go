package redis_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/stackshy/cloudemu/v2/config"
	redisengine "github.com/stackshy/cloudemu/v2/contrib/realengine/redis"
)

// TestRedisProvisionRoundTrip provisions a cache through the engine, connects to
// it with a real Redis client, runs real commands, then deprovisions and
// confirms the server is gone.
func TestRedisProvisionRoundTrip(t *testing.T) {
	eng := redisengine.New()
	t.Cleanup(func() { _ = eng.Close() })

	ctx := context.Background()

	res, err := eng.Provision(ctx, config.CacheProvisionRequest{CacheID: "c1", Engine: "redis"})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}

	rdb := redis.NewClient(&redis.Options{Addr: fmt.Sprintf("%s:%d", res.Host, res.Port)})

	if err := rdb.Set(ctx, "greeting", "cloudemu", 0).Err(); err != nil {
		t.Fatalf("SET: %v", err)
	}

	got, err := rdb.Get(ctx, "greeting").Result()
	if err != nil {
		t.Fatalf("GET: %v", err)
	}

	if got != "cloudemu" {
		t.Fatalf("round-trip mismatch: got %q", got)
	}

	if n, err := rdb.Incr(ctx, "counter").Result(); err != nil || n != 1 {
		t.Fatalf("INCR: n=%d err=%v", n, err)
	}

	_ = rdb.Close()

	if err := eng.Deprovision(ctx, "c1"); err != nil {
		t.Fatalf("Deprovision: %v", err)
	}

	gone := redis.NewClient(&redis.Options{Addr: fmt.Sprintf("%s:%d", res.Host, res.Port)})
	defer gone.Close()

	if err := gone.Ping(ctx).Err(); err == nil {
		t.Fatal("expected connection to the deprovisioned cache to fail")
	}
}
