package redis_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/redis/go-redis/v9"
	"google.golang.org/api/option"
	redisapi "google.golang.org/api/redis/v1"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	redisengine "github.com/stackshy/cloudemu/v2/contrib/realengine/redis"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestGCPMemorystoreRedisE2E runs the real-user flow against GCP Memorystore for
// Redis: create the instance with the real google-cloud client, read its
// host/port, connect with a real Redis client, run real commands, then delete —
// all against CloudEmu backed by a real in-process Redis (no Docker).
func TestGCPMemorystoreRedisE2E(t *testing.T) {
	eng := redisengine.New()
	t.Cleanup(func() { _ = eng.Close() })

	cloudP := cloudemu.NewGCP(config.WithCacheEngine(eng))
	ts := httptest.NewServer(gcpserver.New(gcpserver.Drivers{Memorystore: cloudP.Memorystore}))
	t.Cleanup(ts.Close)

	ctx := context.Background()

	svc, err := redisapi.NewService(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("redis.NewService: %v", err)
	}

	const (
		location = "us-central1"
		instance = "app-cache"
	)

	parent := "projects/mock-project/locations/" + location
	name := parent + "/instances/" + instance

	// 1. Create the instance — like `gcloud redis instances create`.
	op, err := svc.Projects.Locations.Instances.Create(parent, &redisapi.Instance{
		Tier:         "BASIC",
		MemorySizeGb: 1,
	}).InstanceId(instance).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if !op.Done {
		t.Fatalf("create operation not done: %+v", op)
	}

	// 2. Read the endpoint the SDK reports — the real Redis host/port.
	got, err := svc.Projects.Locations.Instances.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Host == "" || got.Port == 0 {
		t.Fatalf("no host/port reported: host=%q port=%d", got.Host, got.Port)
	}

	addr := fmt.Sprintf("%s:%d", got.Host, got.Port)

	// 3. Connect with a real Redis client and run real commands.
	rdb := redis.NewClient(&redis.Options{Addr: addr})

	if err := rdb.Set(ctx, "k", "cloudemu", 0).Err(); err != nil {
		t.Fatalf("SET: %v", err)
	}

	v, err := rdb.Get(ctx, "k").Result()
	if err != nil {
		t.Fatalf("GET: %v", err)
	}

	if v != "cloudemu" {
		t.Fatalf("round-trip mismatch: got %q", v)
	}

	_ = rdb.Close()

	// 4. Delete the instance — the real Redis server is torn down.
	if _, err := svc.Projects.Locations.Instances.Delete(name).Context(ctx).Do(); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}
