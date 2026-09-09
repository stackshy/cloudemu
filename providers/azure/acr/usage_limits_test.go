package acr

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListRegistryUsagesPerSKU confirms the included-storage and
// webhook-count limits reported by ListRegistryUsages follow the registry's
// actual SKU tier (Basic 10 GiB / 2 webhooks, Standard 100 GiB / 10 webhooks,
// Premium 500 GiB / 500 webhooks), per
// https://learn.microsoft.com/azure/container-registry/container-registry-skus,
// rather than a single hardcoded Premium-tier limit for every SKU.
func TestListRegistryUsagesPerSKU(t *testing.T) {
	cases := []struct {
		sku              string
		wantStorageGiB   int64
		wantWebhookQuota int64
	}{
		{sku: "Basic", wantStorageGiB: 10, wantWebhookQuota: 2},
		{sku: "Standard", wantStorageGiB: 100, wantWebhookQuota: 10},
		{sku: "Premium", wantStorageGiB: 500, wantWebhookQuota: 500},
	}

	for _, tc := range cases {
		t.Run(tc.sku, func(t *testing.T) {
			m, _ := newTestMock()
			ctx := context.Background()

			_, _, err := m.CreateOrUpdateRegistry(ctx, "rg-1", "reg1", driver.AzureRegistryConfig{
				Location: "eastus", SKUName: tc.sku,
			})
			require.NoError(t, err)

			usages, err := m.ListRegistryUsages(ctx, "rg-1", "reg1")
			require.NoError(t, err)
			require.Len(t, usages, 2)

			const gib = int64(1) << 30

			var storage, webhooks *driver.AzureRegistryUsage

			for i := range usages {
				switch usages[i].Name {
				case "Size":
					storage = &usages[i]
				case "Webhooks":
					webhooks = &usages[i]
				}
			}

			require.NotNil(t, storage)
			require.NotNil(t, webhooks)

			assert.Equal(t, tc.wantStorageGiB*gib, storage.Limit)
			assert.Equal(t, tc.wantWebhookQuota, webhooks.Limit)
		})
	}
}
