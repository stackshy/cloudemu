package cache_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redis/armredis/v3"
)

const (
	otherRG = "rg-other"
)

// TestSDKAzureCacheDeleteMissingIsIdempotent confirms deleting a cache name
// that was never created succeeds (real ARM DELETE is idempotent — a missing
// resource is a no-op success, not an error), matching every other Azure
// handler in this codebase.
func TestSDKAzureCacheDeleteMissingIsIdempotent(t *testing.T) {
	client := newRedisClient(t)
	ctx := context.Background()

	poller, err := client.BeginDelete(ctx, testRG, "never-existed", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Delete of a never-created cache should be a no-op, got: %v", err)
	}
}

// TestSDKAzureCacheUpdateMissingIs404 confirms PATCH (Redis.Update) on a name
// that was never created fails with 404 rather than silently creating the
// cache — real Azure's Update requires the resource to already exist.
func TestSDKAzureCacheUpdateMissingIs404(t *testing.T) {
	client := newRedisClient(t)
	ctx := context.Background()

	poller, err := client.BeginUpdate(ctx, testRG, "patch-never-created", armredis.UpdateParameters{
		Tags: map[string]*string{"env": to.Ptr("prod")},
	}, nil)

	if err == nil {
		_, err = poller.PollUntilDone(ctx, nil)
	}

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("Update(never-created): got %v, want 404", err)
	}
}

// TestSDKAzureCacheDeleteWrongResourceGroupIsNoop confirms a DELETE issued
// against a URL naming a DIFFERENT resource group than the one a cache was
// actually created in leaves that cache untouched — it must not be reachable,
// let alone deletable, via a resource group it doesn't belong to.
func TestSDKAzureCacheDeleteWrongResourceGroupIsNoop(t *testing.T) {
	client := newRedisClient(t)
	ctx := context.Background()

	createRedis(t, client, testRG, "cross-rg-delete", nil)

	poller, err := client.BeginDelete(ctx, otherRG, "cross-rg-delete", nil)
	if err != nil {
		t.Fatalf("BeginDelete(wrong rg): %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Delete via wrong resource group should be a no-op, got: %v", err)
	}

	got, err := client.Get(ctx, testRG, "cross-rg-delete", nil)
	if err != nil {
		t.Fatalf("cache should still exist in its real resource group: %v", err)
	}

	if got.Name == nil || *got.Name != "cross-rg-delete" {
		t.Fatalf("Get after wrong-rg delete = %v, want cross-rg-delete intact", got.Name)
	}
}

// TestSDKAzureCacheUpdateWrongResourceGroupNotFound confirms a PATCH issued
// against a URL naming a DIFFERENT resource group than the one a cache
// actually belongs to is a 404 — it must not silently re-parent (steal) the
// cache into the URL's resource group.
func TestSDKAzureCacheUpdateWrongResourceGroupNotFound(t *testing.T) {
	client := newRedisClient(t)
	ctx := context.Background()

	createRedis(t, client, testRG, "cross-rg-update", nil)

	poller, err := client.BeginUpdate(ctx, otherRG, "cross-rg-update", armredis.UpdateParameters{
		Tags: map[string]*string{"hijacked": to.Ptr("true")},
	}, nil)

	if err == nil {
		_, err = poller.PollUntilDone(ctx, nil)
	}

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("Update via wrong resource group: got %v, want 404", err)
	}

	got, err := client.Get(ctx, testRG, "cross-rg-update", nil)
	if err != nil {
		t.Fatalf("Get in real resource group: %v", err)
	}

	if _, ok := got.Tags["hijacked"]; ok {
		t.Fatal("cache was mutated by a PATCH issued against a different resource group")
	}
}

// TestSDKAzureCachePutWrongResourceGroupConflict confirms a PUT (BeginCreate)
// for a name already taken by a DIFFERENT resource group's cache is rejected
// as a conflict — Redis cache names are globally unique (they get a public DNS
// hostname), so this scope cannot "adopt" another group's cache by re-PUTting
// its name.
func TestSDKAzureCachePutWrongResourceGroupConflict(t *testing.T) {
	client := newRedisClient(t)
	ctx := context.Background()

	createRedis(t, client, testRG, "cross-rg-put", nil)

	poller, err := client.BeginCreate(ctx, otherRG, "cross-rg-put", armredis.CreateParameters{
		Location: to.Ptr("eastus"),
		Properties: &armredis.CreateProperties{
			SKU: &armredis.SKU{
				Name:     to.Ptr(armredis.SKUNameStandard),
				Family:   to.Ptr(armredis.SKUFamilyC),
				Capacity: to.Ptr(int32(1)),
			},
		},
	}, nil)

	if err == nil {
		_, err = poller.PollUntilDone(ctx, nil)
	}

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 409 {
		t.Fatalf("Create via wrong resource group for a taken name: got %v, want 409", err)
	}

	got, err := client.Get(ctx, testRG, "cross-rg-put", nil)
	if err != nil {
		t.Fatalf("Get in real resource group: %v", err)
	}

	if got.Name == nil || *got.Name != "cross-rg-put" {
		t.Fatalf("original cache should be untouched, got %v", got.Name)
	}
}
