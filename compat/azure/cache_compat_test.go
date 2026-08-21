package azure

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redis/armredis/v3"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const (
	cacheSub     = "sub-1"
	cacheRG      = "rg-1"
	cacheName    = "compat-cache"
	cacheSKUCap  = int32(1)
	cacheService = "cache"
)

// newCacheClient builds a real armredis client pointed at the emulator's TLS
// wire server. Azure ARM SDKs refuse bearer tokens over plaintext, so the
// session is booted over TLS (BootAzureTLS) and paired with FakeAzureCred; the
// ResourceManager endpoint is overridden to the emulator's URL.
func newCacheClient(t *testing.T, sess *compat.AzureSession) *armredis.Client {
	t.Helper()

	emu := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Endpoint: sess.Endpoint(),
				Audience: "https://management.azure.com",
			},
		},
	}

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     emu,
			Transport: sess.Transport(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	cf, err := armredis.NewClientFactory(cacheSub, compat.FakeAzureCred(), opts)
	if err != nil {
		t.Fatalf("armredis.NewClientFactory: %v", err)
	}

	return cf.NewClient()
}

// TestCompatAzureCache drives the real azure-sdk-for-go armredis client against
// CloudEmu's Microsoft.Cache/redis ARM wire handler and records one compat
// result per portable control-plane op (CreateCache, GetCache, ListCaches,
// UpdateCache, DeleteCache).
func TestCompatAzureCache(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	sess := compat.BootAzureTLS(t, azureserver.Drivers{Cache: cloudP.Cache})
	client := newCacheClient(t, sess)
	ctx := context.Background()

	sess.Op(cacheService, "CreateCache", func() error {
		poller, err := client.BeginCreate(ctx, cacheRG, cacheName, armredis.CreateParameters{
			Location: to.Ptr("eastus"),
			Tags:     map[string]*string{"env": to.Ptr("test")},
			Properties: &armredis.CreateProperties{
				SKU: &armredis.SKU{
					Name:     to.Ptr(armredis.SKUNameStandard),
					Family:   to.Ptr(armredis.SKUFamilyC),
					Capacity: to.Ptr(cacheSKUCap),
				},
			},
		}, nil)
		if err != nil {
			return err
		}

		_, err = poller.PollUntilDone(ctx, nil)

		return err
	})

	sess.Op(cacheService, "GetCache", func() error {
		_, err := client.Get(ctx, cacheRG, cacheName, nil)

		return err
	})

	sess.Op(cacheService, "UpdateCache", func() error {
		// The real armredis BeginUpdate issues a PATCH; the ARM handler routes it
		// to the create-or-update path.
		poller, err := client.BeginUpdate(ctx, cacheRG, cacheName, armredis.UpdateParameters{
			Tags: map[string]*string{"env": to.Ptr("prod")},
			Properties: &armredis.UpdateProperties{
				SKU: &armredis.SKU{
					Name:     to.Ptr(armredis.SKUNameStandard),
					Family:   to.Ptr(armredis.SKUFamilyC),
					Capacity: to.Ptr(cacheSKUCap),
				},
			},
		}, nil)
		if err != nil {
			return err
		}

		_, err = poller.PollUntilDone(ctx, nil)

		return err
	})

	sess.Op(cacheService, "ListCaches", func() error {
		pager := client.NewListByResourceGroupPager(cacheRG, nil)
		for pager.More() {
			if _, err := pager.NextPage(ctx); err != nil {
				return err
			}
		}

		return nil
	})

	sess.Op(cacheService, "DeleteCache", func() error {
		poller, err := client.BeginDelete(ctx, cacheRG, cacheName, nil)
		if err != nil {
			return err
		}

		_, err = poller.PollUntilDone(ctx, nil)

		return err
	})
}
