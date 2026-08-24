package cache_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redis/armredis/v3"
)

// createCache is a small helper that provisions a Standard cache for the
// access-key tests.
func createCache(t *testing.T, client *armredis.Client, name string) {
	t.Helper()

	poller, err := client.BeginCreate(context.Background(), testRG, name, armredis.CreateParameters{
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

	if _, err := poller.PollUntilDone(context.Background(), nil); err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}
}

// TestSDKAzureCacheListKeys verifies ListKeys returns both access keys — the
// primary way a client fetches the credential needed to connect to the cache.
func TestSDKAzureCacheListKeys(t *testing.T) {
	client := newRedisClient(t)
	ctx := context.Background()

	createCache(t, client, "keys-cache")

	got, err := client.ListKeys(ctx, testRG, "keys-cache", nil)
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}

	if got.PrimaryKey == nil || *got.PrimaryKey == "" {
		t.Errorf("primaryKey = %v, want non-empty", got.PrimaryKey)
	}

	if got.SecondaryKey == nil || *got.SecondaryKey == "" {
		t.Errorf("secondaryKey = %v, want non-empty", got.SecondaryKey)
	}

	if got.PrimaryKey != nil && got.SecondaryKey != nil && *got.PrimaryKey == *got.SecondaryKey {
		t.Error("primary and secondary keys must differ")
	}
}

// TestSDKAzureCacheListKeysNotFound confirms ListKeys on a missing cache is a
// 404, not a 200 with empty keys.
func TestSDKAzureCacheListKeysNotFound(t *testing.T) {
	client := newRedisClient(t)

	_, err := client.ListKeys(context.Background(), testRG, "missing", nil)
	if err == nil {
		t.Fatal("ListKeys(missing): got nil error, want 404")
	}
}

// TestSDKAzureCacheRegenerateKey verifies RegenerateKey rotates only the
// requested key: regenerating Primary changes the primary key while the
// secondary is untouched, and both current keys come back in the response.
func TestSDKAzureCacheRegenerateKey(t *testing.T) {
	client := newRedisClient(t)
	ctx := context.Background()

	createCache(t, client, "regen-cache")

	before, err := client.ListKeys(ctx, testRG, "regen-cache", nil)
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}

	after, err := client.RegenerateKey(ctx, testRG, "regen-cache", armredis.RegenerateKeyParameters{
		KeyType: to.Ptr(armredis.RedisKeyTypePrimary),
	}, nil)
	if err != nil {
		t.Fatalf("RegenerateKey: %v", err)
	}

	if after.PrimaryKey == nil || *after.PrimaryKey == *before.PrimaryKey {
		t.Errorf("primary key was not rotated: before=%v after=%v", before.PrimaryKey, after.PrimaryKey)
	}

	if after.SecondaryKey == nil || *after.SecondaryKey != *before.SecondaryKey {
		t.Errorf("secondary key changed unexpectedly: before=%v after=%v", before.SecondaryKey, after.SecondaryKey)
	}

	// A subsequent ListKeys must reflect the rotated primary.
	relisted, err := client.ListKeys(ctx, testRG, "regen-cache", nil)
	if err != nil {
		t.Fatalf("ListKeys after regenerate: %v", err)
	}

	if relisted.PrimaryKey == nil || *relisted.PrimaryKey != *after.PrimaryKey {
		t.Errorf("listKeys primary = %v, want %v", relisted.PrimaryKey, after.PrimaryKey)
	}
}

// TestSDKAzureCacheRegenerateSecondary confirms rotating Secondary leaves the
// primary key unchanged.
func TestSDKAzureCacheRegenerateSecondary(t *testing.T) {
	client := newRedisClient(t)
	ctx := context.Background()

	createCache(t, client, "regen-sec")

	before, err := client.ListKeys(ctx, testRG, "regen-sec", nil)
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}

	after, err := client.RegenerateKey(ctx, testRG, "regen-sec", armredis.RegenerateKeyParameters{
		KeyType: to.Ptr(armredis.RedisKeyTypeSecondary),
	}, nil)
	if err != nil {
		t.Fatalf("RegenerateKey: %v", err)
	}

	if after.PrimaryKey == nil || *after.PrimaryKey != *before.PrimaryKey {
		t.Errorf("primary key changed unexpectedly: before=%v after=%v", before.PrimaryKey, after.PrimaryKey)
	}

	if after.SecondaryKey == nil || *after.SecondaryKey == *before.SecondaryKey {
		t.Errorf("secondary key was not rotated: before=%v after=%v", before.SecondaryKey, after.SecondaryKey)
	}
}

// TestSDKAzureCacheCreateReturnsKeys verifies the Create response carries
// properties.accessKeys (real Azure populates them only on create/update).
func TestSDKAzureCacheCreateReturnsKeys(t *testing.T) {
	client := newRedisClient(t)
	ctx := context.Background()

	poller, err := client.BeginCreate(ctx, testRG, "create-keys", armredis.CreateParameters{
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

	created, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	if created.Properties == nil || created.Properties.AccessKeys == nil {
		t.Fatalf("expected accessKeys on create response, got %+v", created.Properties)
	}

	if created.Properties.AccessKeys.PrimaryKey == nil || *created.Properties.AccessKeys.PrimaryKey == "" {
		t.Errorf("create accessKeys.primaryKey = %v, want non-empty", created.Properties.AccessKeys.PrimaryKey)
	}
}
