package cache_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redis/armredis/v3"
)

// TestSDKAzureCacheLocationRoundTrip verifies the region supplied at create time
// is returned on Get, rather than a hardcoded default.
func TestSDKAzureCacheLocationRoundTrip(t *testing.T) {
	client := newRedisClient(t)
	ctx := context.Background()

	poller, err := client.BeginCreate(ctx, testRG, "loc-cache", armredis.CreateParameters{
		Location: to.Ptr("westus2"),
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

	created, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	if created.Location == nil || *created.Location != "westus2" {
		t.Errorf("create location = %v, want westus2", created.Location)
	}

	got, err := client.Get(ctx, testRG, "loc-cache", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Location == nil || *got.Location != "westus2" {
		t.Errorf("get location = %v, want westus2", got.Location)
	}
}

// TestSDKAzureCachePorts verifies both the non-SSL port (6379) and the SSL port
// (6380) are populated on the Get response.
func TestSDKAzureCachePorts(t *testing.T) {
	client := newRedisClient(t)
	ctx := context.Background()

	createCache(t, client, "ports-cache")

	got, err := client.Get(ctx, testRG, "ports-cache", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties == nil {
		t.Fatal("expected properties on response")
	}

	if got.Properties.Port == nil || *got.Properties.Port != 6379 {
		t.Errorf("port = %v, want 6379", got.Properties.Port)
	}

	if got.Properties.SSLPort == nil || *got.Properties.SSLPort != 6380 {
		t.Errorf("sslPort = %v, want 6380", got.Properties.SSLPort)
	}
}

// TestSDKAzureCacheGetWrongResourceGroup confirms a cache created in one
// resource group does not resolve under a different group in the request path —
// real ARM answers 404 because the returned id would contradict the path.
func TestSDKAzureCacheGetWrongResourceGroup(t *testing.T) {
	client := newRedisClient(t)
	ctx := context.Background()

	createCache(t, client, "scoped-cache")

	_, err := client.Get(ctx, "rg-other", "scoped-cache", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("Get(wrong rg): got %v, want 404", err)
	}
}
