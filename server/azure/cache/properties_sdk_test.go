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

// TestSDKAzureCacheRedisPropertiesRoundTrip verifies the top-level Redis
// properties that azurerm_redis_cache sets — redisConfiguration (typed and
// passthrough keys), enableNonSslPort, minimumTlsVersion, publicNetworkAccess,
// and redisVersion — are echoed back on Get instead of being dropped, which
// would otherwise cause a perpetual Terraform diff.
func TestSDKAzureCacheRedisPropertiesRoundTrip(t *testing.T) {
	client := newRedisClient(t)
	ctx := context.Background()

	poller, err := client.BeginCreate(ctx, testRG, "props-cache", armredis.CreateParameters{
		Location: to.Ptr("eastus"),
		Properties: &armredis.CreateProperties{
			SKU: &armredis.SKU{
				Name:     to.Ptr(armredis.SKUNameStandard),
				Family:   to.Ptr(armredis.SKUFamilyC),
				Capacity: to.Ptr(int32(1)),
			},
			RedisConfiguration: &armredis.CommonPropertiesRedisConfiguration{
				MaxmemoryPolicy:      to.Ptr("allkeys-lru"),
				MaxmemoryReserved:    to.Ptr("50"),
				AdditionalProperties: map[string]any{"custom-passthrough-key": "kept"},
			},
			EnableNonSSLPort:    to.Ptr(true),
			MinimumTLSVersion:   to.Ptr(armredis.TLSVersionOne2),
			PublicNetworkAccess: to.Ptr(armredis.PublicNetworkAccessDisabled),
			RedisVersion:        to.Ptr("6.0"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	got, err := client.Get(ctx, testRG, "props-cache", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	props := got.Properties
	if props == nil {
		t.Fatal("expected properties on response")
	}

	assertRedisConfig(t, props.RedisConfiguration)

	if props.EnableNonSSLPort == nil || !*props.EnableNonSSLPort {
		t.Errorf("enableNonSslPort = %v, want true", props.EnableNonSSLPort)
	}

	if props.MinimumTLSVersion == nil || *props.MinimumTLSVersion != armredis.TLSVersionOne2 {
		t.Errorf("minimumTlsVersion = %v, want 1.2", props.MinimumTLSVersion)
	}

	if props.PublicNetworkAccess == nil || *props.PublicNetworkAccess != armredis.PublicNetworkAccessDisabled {
		t.Errorf("publicNetworkAccess = %v, want Disabled", props.PublicNetworkAccess)
	}

	if props.RedisVersion == nil || *props.RedisVersion != "6.0" {
		t.Errorf("redisVersion = %v, want 6.0", props.RedisVersion)
	}
}

// assertRedisConfig checks both a typed key (maxmemory-policy) and an
// unmodeled passthrough key (maxmemory-reserved, decoded into
// AdditionalProperties) survive the round-trip.
func assertRedisConfig(t *testing.T, cfg *armredis.CommonPropertiesRedisConfiguration) {
	t.Helper()

	if cfg == nil {
		t.Fatal("expected redisConfiguration on response")
	}

	if cfg.MaxmemoryPolicy == nil || *cfg.MaxmemoryPolicy != "allkeys-lru" {
		t.Errorf("maxmemory-policy = %v, want allkeys-lru", cfg.MaxmemoryPolicy)
	}

	if cfg.MaxmemoryReserved == nil || *cfg.MaxmemoryReserved != "50" {
		t.Errorf("maxmemory-reserved = %v, want 50", cfg.MaxmemoryReserved)
	}

	if got, _ := cfg.AdditionalProperties["custom-passthrough-key"].(string); got != "kept" {
		t.Errorf("custom-passthrough-key = %v, want kept", cfg.AdditionalProperties["custom-passthrough-key"])
	}
}

// TestSDKAzureCacheScaleUpdatePreservesProperties verifies a partial PATCH that
// only scales capacity does NOT wipe the previously-set redisConfiguration and
// enableNonSslPort — the nil-mask discipline in UpdateCache.
func TestSDKAzureCacheScaleUpdatePreservesProperties(t *testing.T) {
	client := newRedisClient(t)
	ctx := context.Background()

	cPoller, err := client.BeginCreate(ctx, testRG, "scale-cache", armredis.CreateParameters{
		Location: to.Ptr("eastus"),
		Properties: &armredis.CreateProperties{
			SKU: &armredis.SKU{
				Name:     to.Ptr(armredis.SKUNameStandard),
				Family:   to.Ptr(armredis.SKUFamilyC),
				Capacity: to.Ptr(int32(1)),
			},
			RedisConfiguration: &armredis.CommonPropertiesRedisConfiguration{
				MaxmemoryPolicy: to.Ptr("allkeys-lru"),
			},
			EnableNonSSLPort: to.Ptr(true),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate: %v", err)
	}

	if _, err := cPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Create PollUntilDone: %v", err)
	}

	// Scale-only PATCH: bump capacity, supply no redisConfiguration/enableNonSslPort.
	uPoller, err := client.BeginUpdate(ctx, testRG, "scale-cache", armredis.UpdateParameters{
		Properties: &armredis.UpdateProperties{
			SKU: &armredis.SKU{
				Name:     to.Ptr(armredis.SKUNameStandard),
				Family:   to.Ptr(armredis.SKUFamilyC),
				Capacity: to.Ptr(int32(2)),
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}

	if _, err := uPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Update PollUntilDone: %v", err)
	}

	got, err := client.Get(ctx, testRG, "scale-cache", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	props := got.Properties
	if props == nil || props.SKU == nil || props.SKU.Capacity == nil || *props.SKU.Capacity != 2 {
		t.Fatalf("capacity not scaled to 2: %+v", props)
	}

	if props.RedisConfiguration == nil || props.RedisConfiguration.MaxmemoryPolicy == nil ||
		*props.RedisConfiguration.MaxmemoryPolicy != "allkeys-lru" {
		t.Errorf("scale-only PATCH wiped redisConfiguration: %+v", props.RedisConfiguration)
	}

	if props.EnableNonSSLPort == nil || !*props.EnableNonSSLPort {
		t.Errorf("scale-only PATCH wiped enableNonSslPort: %v", props.EnableNonSSLPort)
	}
}
