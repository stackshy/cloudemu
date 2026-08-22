package realengine_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redis/armredis/v3"
	"github.com/redis/go-redis/v9"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/realengine"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestAzureCacheRedisE2E runs the real-user flow against Azure Cache for Redis:
// create the cache with the real Azure SDK, read its host/port, connect with a
// real Redis client, run real commands, then delete — all against CloudEmu
// backed by a real in-process Redis (no Docker, no cloud account).
func TestAzureCacheRedisE2E(t *testing.T) {
	eng := realengine.NewRedis()
	t.Cleanup(func() { _ = eng.Close() })

	cloudP := cloudemu.NewAzure(config.WithCacheEngine(eng))
	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{Cache: cloudP.Cache}))
	t.Cleanup(ts.Close)

	client, err := armredis.NewClient("sub-1", azureFakeCred{}, armOpts(ts))
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	ctx := context.Background()

	const (
		rg    = "rg-1"
		cache = "app-cache"
	)

	// 1. Create the cache — like `az redis create`.
	createPoller, err := client.BeginCreate(ctx, rg, cache, armredis.CreateParameters{
		Location: to.Ptr("eastus"),
		Properties: &armredis.CreateProperties{
			SKU: &armredis.SKU{
				Name:     to.Ptr(armredis.SKUNameStandard),
				Family:   to.Ptr(armredis.SKUFamilyC),
				Capacity: to.Ptr(int32(1)),
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate: %v", err)
	}

	if _, err := createPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("create poll: %v", err)
	}

	// 2. Read the endpoint the SDK reports — the real Redis host/port.
	got, err := client.Get(ctx, rg, cache, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties == nil || got.Properties.HostName == nil || got.Properties.SSLPort == nil {
		t.Fatalf("no host/port reported: %+v", got.ResourceInfo)
	}

	addr := fmt.Sprintf("%s:%d", *got.Properties.HostName, *got.Properties.SSLPort)

	// 3. Connect with a real Redis client and run real commands.
	rdb := redis.NewClient(&redis.Options{Addr: addr})

	if err := rdb.Set(ctx, "user:1", "cloudemu", 0).Err(); err != nil {
		t.Fatalf("SET: %v", err)
	}

	v, err := rdb.Get(ctx, "user:1").Result()
	if err != nil {
		t.Fatalf("GET: %v", err)
	}

	if v != "cloudemu" {
		t.Fatalf("round-trip mismatch: got %q", v)
	}

	if n, err := rdb.Incr(ctx, "count").Result(); err != nil || n != 1 {
		t.Fatalf("INCR: n=%d err=%v", n, err)
	}

	_ = rdb.Close()

	// 4. Delete the cache — the real Redis server is torn down.
	delPoller, err := client.BeginDelete(ctx, rg, cache, nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("delete poll: %v", err)
	}
}
