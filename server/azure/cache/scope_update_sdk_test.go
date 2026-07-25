package cache_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redis/armredis/v3"
)

func createRedis(t *testing.T, client *armredis.Client, rg, name string, tags map[string]*string) armredis.ResourceInfo {
	t.Helper()
	ctx := context.Background()

	poller, err := client.BeginCreate(ctx, rg, name, armredis.CreateParameters{
		Location: to.Ptr("eastus"),
		Tags:     tags,
		Properties: &armredis.CreateProperties{
			SKU: &armredis.SKU{
				Name:     to.Ptr(armredis.SKUNameStandard),
				Family:   to.Ptr(armredis.SKUFamilyC),
				Capacity: to.Ptr(int32(1)),
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate %s/%s: %v", rg, name, err)
	}
	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("poll %s/%s: %v", rg, name, err)
	}
	return res.ResourceInfo
}

// TestSDKScopedListing asserts #259 item 1 through the real SDK: caches
// created in one resource group must not appear in another group's list.
func TestSDKScopedListing(t *testing.T) {
	client := newRedisClient(t)
	ctx := context.Background()

	createRedis(t, client, "rg-team-a", "cache-a1", nil)
	createRedis(t, client, "rg-team-a", "cache-a2", nil)
	createRedis(t, client, "rg-team-b", "cache-b1", nil)

	listRG := func(rg string) []string {
		var names []string
		pager := client.NewListByResourceGroupPager(rg, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				t.Fatalf("list %s: %v", rg, err)
			}
			for _, c := range page.Value {
				names = append(names, *c.Name)
			}
		}
		return names
	}

	gotA := listRG("rg-team-a")
	if len(gotA) != 2 {
		t.Fatalf("rg-team-a listed %v, want exactly its own 2 caches", gotA)
	}
	gotB := listRG("rg-team-b")
	if len(gotB) != 1 || gotB[0] != "cache-b1" {
		t.Fatalf("rg-team-b listed %v, want [cache-b1]", gotB)
	}
}

// TestSDKUpsertAppliesUpdates asserts #259 item 2 through the real SDK:
// BeginCreate on an existing cache must apply the request's tags, not echo
// the stale resource.
func TestSDKUpsertAppliesUpdates(t *testing.T) {
	client := newRedisClient(t)
	ctx := context.Background()

	createRedis(t, client, testRG, "cache-upsert", map[string]*string{"env": to.Ptr("dev")})

	updated := createRedis(t, client, testRG, "cache-upsert",
		map[string]*string{"env": to.Ptr("prod"), "team": to.Ptr("core")})
	if updated.Tags["env"] == nil || *updated.Tags["env"] != "prod" {
		t.Fatalf("upsert response tags = %v, want env=prod applied", updated.Tags)
	}

	got, err := client.Get(ctx, testRG, "cache-upsert", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tags["env"] == nil || *got.Tags["env"] != "prod" || got.Tags["team"] == nil {
		t.Fatalf("stored tags = %v, want env=prod team=core (CreateOrUpdate must not discard updates)", got.Tags)
	}
}

// TestSDKResourceIDMatchesRequestScope asserts #259 item 4 through the real
// SDK: the returned ARM id carries the request's subscription and resource
// group, not a hardcoded default.
func TestSDKResourceIDMatchesRequestScope(t *testing.T) {
	client := newRedisClient(t)

	created := createRedis(t, client, "rg-id-check", "cache-id", nil)
	if created.ID == nil {
		t.Fatal("cache response has no id")
	}
	id := *created.ID
	if !strings.Contains(id, "/subscriptions/"+testSub+"/") || !strings.Contains(id, "/resourceGroups/rg-id-check/") {
		t.Fatalf("id = %q, want it under subscription %q and resource group rg-id-check", id, testSub)
	}
}
