package resourcediscovery

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/elasticache"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
)

func TestWalkCache(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions(config.WithClock(config.NewFakeClock(time.Unix(0, 0))))
	c := elasticache.New(opts)

	if _, err := c.CreateCache(ctx, cachedriver.CacheConfig{Name: "sessions", NodeType: "cache.t3.micro", Engine: "redis"}); err != nil {
		t.Fatalf("create cache: %v", err)
	}

	eng := New(ProviderAWS, "123456789012", "us-east-1", &Drivers{Cache: c})

	res, err := eng.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	var found int
	for i := range res {
		if res[i].Service == ServiceCache && res[i].Type == TypeCacheCluster {
			found++
			if res[i].ID != "sessions" {
				t.Fatalf("cache ID = %q, want sessions", res[i].ID)
			}
		}
	}

	if found != 1 {
		t.Fatalf("expected 1 discovered cache, got %d (of %d resources)", found, len(res))
	}
}
