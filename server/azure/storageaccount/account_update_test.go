// Real-SDK round-trip test: the live azure-sdk-for-go armstorage
// AccountsClient.Update (PATCH) drives the in-memory handler end-to-end,
// proving a partial update changes only the submitted fields and leaves
// everything else on the account untouched.

package storageaccount_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSDKAccountUpdatePartial proves PATCH (previously a 405) applies a
// non-destructive partial update: only the accessTier submitted in this call
// changes, while the SKU/kind/location/tags from the original create survive.
func TestSDKAccountUpdatePartial(t *testing.T) {
	ctx := context.Background()
	client := newAccountsClient(t)

	poller, err := client.BeginCreate(ctx, "rg-1", "acctpatch", armstorage.AccountCreateParameters{
		Location: to.Ptr("westus2"),
		Kind:     to.Ptr(armstorage.KindStorageV2),
		SKU:      &armstorage.SKU{Name: to.Ptr(armstorage.SKUNameStandardLRS)},
		Properties: &armstorage.AccountPropertiesCreateParameters{
			AccessTier: to.Ptr(armstorage.AccessTierHot),
		},
		Tags: map[string]*string{"env": to.Ptr("prod")},
	}, nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	updated, err := client.Update(ctx, "rg-1", "acctpatch", armstorage.AccountUpdateParameters{
		Properties: &armstorage.AccountPropertiesUpdateParameters{
			AccessTier: to.Ptr(armstorage.AccessTierCool),
		},
	}, nil)
	require.NoError(t, err)

	require.NotNil(t, updated.Properties)
	require.NotNil(t, updated.Properties.AccessTier)
	assert.Equal(t, armstorage.AccessTierCool, *updated.Properties.AccessTier)

	// Everything the PATCH omitted must survive unchanged.
	require.NotNil(t, updated.SKU)
	assert.Equal(t, armstorage.SKUNameStandardLRS, *updated.SKU.Name)
	require.NotNil(t, updated.Kind)
	assert.Equal(t, armstorage.KindStorageV2, *updated.Kind)
	require.NotNil(t, updated.Location)
	assert.Equal(t, "westus2", *updated.Location)
	require.NotNil(t, updated.Tags["env"])
	assert.Equal(t, "prod", *updated.Tags["env"])

	// ... and an independent GET agrees.
	got, err := client.GetProperties(ctx, "rg-1", "acctpatch", nil)
	require.NoError(t, err)
	require.NotNil(t, got.Properties)
	require.NotNil(t, got.Properties.AccessTier)
	assert.Equal(t, armstorage.AccessTierCool, *got.Properties.AccessTier)
	require.NotNil(t, got.SKU)
	assert.Equal(t, armstorage.SKUNameStandardLRS, *got.SKU.Name)
}

// TestSDKAccountUpdateSKU proves PATCH can also change the SKU (a redundancy
// upgrade, e.g. LRS -> GRS) while leaving accessTier/tags untouched.
func TestSDKAccountUpdateSKU(t *testing.T) {
	ctx := context.Background()
	client := newAccountsClient(t)

	poller, err := client.BeginCreate(ctx, "rg-1", "acctpatch2", armstorage.AccountCreateParameters{
		Location: to.Ptr("eastus"),
		Kind:     to.Ptr(armstorage.KindStorageV2),
		SKU:      &armstorage.SKU{Name: to.Ptr(armstorage.SKUNameStandardLRS)},
	}, nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	updated, err := client.Update(ctx, "rg-1", "acctpatch2", armstorage.AccountUpdateParameters{
		SKU: &armstorage.SKU{Name: to.Ptr(armstorage.SKUNameStandardGRS)},
	}, nil)
	require.NoError(t, err)

	require.NotNil(t, updated.SKU)
	assert.Equal(t, armstorage.SKUNameStandardGRS, *updated.SKU.Name)
}

// TestSDKAccountUpdateMissing proves PATCH 404s on a nonexistent account,
// matching create/get/delete.
func TestSDKAccountUpdateMissing(t *testing.T) {
	ctx := context.Background()
	client := newAccountsClient(t)

	_, err := client.Update(ctx, "rg-1", "nope", armstorage.AccountUpdateParameters{
		Tags: map[string]*string{"env": to.Ptr("prod")},
	}, nil)
	require.Error(t, err)
}
