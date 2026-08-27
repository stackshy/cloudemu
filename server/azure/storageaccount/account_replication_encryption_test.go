// Real-SDK round-trip tests: the live azure-sdk-for-go armstorage
// AccountsClient proves (1) a GRS/RA-GRS/GZRS/RA-GZRS account reports a
// secondary location/status/endpoints, an LRS account does not, and (2) a
// customer-managed-key (Microsoft.Keyvault) encryption request is echoed
// back consistently instead of always reporting the platform-managed
// default.

package storageaccount_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSDKAccountLRSHasNoSecondaryRegion proves a plain LRS account (the
// default) reports no secondary location/status/endpoints — those fields
// only apply to geo-redundant SKUs.
func TestSDKAccountLRSHasNoSecondaryRegion(t *testing.T) {
	ctx := context.Background()
	client := newAccountsClient(t)

	poller, err := client.BeginCreate(ctx, "rg-1", "acctlrs", armstorage.AccountCreateParameters{
		Location: to.Ptr("eastus"),
		Kind:     to.Ptr(armstorage.KindStorageV2),
		SKU:      &armstorage.SKU{Name: to.Ptr(armstorage.SKUNameStandardLRS)},
	}, nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	got, err := client.GetProperties(ctx, "rg-1", "acctlrs", nil)
	require.NoError(t, err)

	require.NotNil(t, got.Properties)
	assert.Nil(t, got.Properties.SecondaryLocation)
	assert.Nil(t, got.Properties.StatusOfSecondary)
	assert.Nil(t, got.Properties.SecondaryEndpoints)
}

// TestSDKAccountGRSHasSecondaryRegion proves a GRS account reports its
// secondary location and status, but — lacking read access — no secondary
// endpoints.
func TestSDKAccountGRSHasSecondaryRegion(t *testing.T) {
	ctx := context.Background()
	client := newAccountsClient(t)

	poller, err := client.BeginCreate(ctx, "rg-1", "acctgrs", armstorage.AccountCreateParameters{
		Location: to.Ptr("eastus"),
		Kind:     to.Ptr(armstorage.KindStorageV2),
		SKU:      &armstorage.SKU{Name: to.Ptr(armstorage.SKUNameStandardGRS)},
	}, nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	got, err := client.GetProperties(ctx, "rg-1", "acctgrs", nil)
	require.NoError(t, err)

	require.NotNil(t, got.Properties)
	require.NotNil(t, got.Properties.SecondaryLocation)
	assert.Equal(t, "westus", *got.Properties.SecondaryLocation)
	require.NotNil(t, got.Properties.StatusOfSecondary)
	assert.Equal(t, armstorage.AccountStatusAvailable, *got.Properties.StatusOfSecondary)
	assert.Nil(t, got.Properties.SecondaryEndpoints, "plain GRS has no read-access secondary endpoints")
}

// TestSDKAccountRAGRSHasSecondaryEndpoints proves an RA-GRS account
// additionally reports readable secondary endpoints, at the real "-secondary"
// hostname convention.
func TestSDKAccountRAGRSHasSecondaryEndpoints(t *testing.T) {
	ctx := context.Background()
	client := newAccountsClient(t)

	poller, err := client.BeginCreate(ctx, "rg-1", "acctragrs", armstorage.AccountCreateParameters{
		Location: to.Ptr("westeurope"),
		Kind:     to.Ptr(armstorage.KindStorageV2),
		SKU:      &armstorage.SKU{Name: to.Ptr(armstorage.SKUNameStandardRAGRS)},
	}, nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	got, err := client.GetProperties(ctx, "rg-1", "acctragrs", nil)
	require.NoError(t, err)

	require.NotNil(t, got.Properties)
	require.NotNil(t, got.Properties.SecondaryLocation)
	assert.Equal(t, "northeurope", *got.Properties.SecondaryLocation)
	require.NotNil(t, got.Properties.SecondaryEndpoints)
	require.NotNil(t, got.Properties.SecondaryEndpoints.Blob)
	assert.Contains(t, *got.Properties.SecondaryEndpoints.Blob, "acctragrs-secondary.blob.core.windows.net")
}

// TestSDKAccountCMKEncryptionEchoed proves a customer-managed-key encryption
// request survives create and an independent GET, instead of always
// reporting the Microsoft.Storage platform default.
func TestSDKAccountCMKEncryptionEchoed(t *testing.T) {
	ctx := context.Background()
	client := newAccountsClient(t)

	poller, err := client.BeginCreate(ctx, "rg-1", "acctcmk", armstorage.AccountCreateParameters{
		Location: to.Ptr("eastus"),
		Kind:     to.Ptr(armstorage.KindStorageV2),
		SKU:      &armstorage.SKU{Name: to.Ptr(armstorage.SKUNameStandardLRS)},
		Properties: &armstorage.AccountPropertiesCreateParameters{
			Encryption: &armstorage.Encryption{
				KeySource: to.Ptr(armstorage.KeySourceMicrosoftKeyvault),
				KeyVaultProperties: &armstorage.KeyVaultProperties{
					KeyVaultURI: to.Ptr("https://myvault.vault.azure.net"),
					KeyName:     to.Ptr("mykey"),
					KeyVersion:  to.Ptr("v1"),
				},
			},
		},
	}, nil)
	require.NoError(t, err)

	created, err := poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)
	assertCMKEncryption(t, created.Properties)

	got, err := client.GetProperties(ctx, "rg-1", "acctcmk", nil)
	require.NoError(t, err)
	assertCMKEncryption(t, got.Properties)
}

func assertCMKEncryption(t *testing.T, props *armstorage.AccountProperties) {
	t.Helper()

	require.NotNil(t, props)
	require.NotNil(t, props.Encryption)
	require.NotNil(t, props.Encryption.KeySource)
	assert.Equal(t, armstorage.KeySourceMicrosoftKeyvault, *props.Encryption.KeySource)
	require.NotNil(t, props.Encryption.KeyVaultProperties)
	require.NotNil(t, props.Encryption.KeyVaultProperties.KeyVaultURI)
	assert.Equal(t, "https://myvault.vault.azure.net", *props.Encryption.KeyVaultProperties.KeyVaultURI)
	require.NotNil(t, props.Encryption.KeyVaultProperties.KeyName)
	assert.Equal(t, "mykey", *props.Encryption.KeyVaultProperties.KeyName)
}

// TestSDKAccountDefaultEncryptionUnchanged proves an account created without
// an encryption request still reports the platform-managed default — the CMK
// fix must not regress the common case.
func TestSDKAccountDefaultEncryptionUnchanged(t *testing.T) {
	ctx := context.Background()
	client := newAccountsClient(t)

	poller, err := client.BeginCreate(ctx, "rg-1", "acctdefaultenc", armstorage.AccountCreateParameters{
		Location: to.Ptr("eastus"),
		Kind:     to.Ptr(armstorage.KindStorageV2),
		SKU:      &armstorage.SKU{Name: to.Ptr(armstorage.SKUNameStandardLRS)},
	}, nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	got, err := client.GetProperties(ctx, "rg-1", "acctdefaultenc", nil)
	require.NoError(t, err)

	require.NotNil(t, got.Properties)
	require.NotNil(t, got.Properties.Encryption)
	require.NotNil(t, got.Properties.Encryption.KeySource)
	assert.Equal(t, armstorage.KeySourceMicrosoftStorage, *got.Properties.Encryption.KeySource)
	assert.Nil(t, got.Properties.Encryption.KeyVaultProperties)
}

// TestSDKAccountUpdatePreservesEncryptionWhenOmitted proves a PATCH that
// doesn't submit an encryption block leaves a previously configured
// customer-managed key untouched — encryption lives in its own store
// precisely so an unrelated PATCH (e.g. only changing tags) can't blindly
// reset it.
func TestSDKAccountUpdatePreservesEncryptionWhenOmitted(t *testing.T) {
	ctx := context.Background()
	client := newAccountsClient(t)

	poller, err := client.BeginCreate(ctx, "rg-1", "acctcmkpatch", armstorage.AccountCreateParameters{
		Location: to.Ptr("eastus"),
		Kind:     to.Ptr(armstorage.KindStorageV2),
		SKU:      &armstorage.SKU{Name: to.Ptr(armstorage.SKUNameStandardLRS)},
		Properties: &armstorage.AccountPropertiesCreateParameters{
			Encryption: &armstorage.Encryption{
				KeySource: to.Ptr(armstorage.KeySourceMicrosoftKeyvault),
				KeyVaultProperties: &armstorage.KeyVaultProperties{
					KeyVaultURI: to.Ptr("https://myvault.vault.azure.net"),
					KeyName:     to.Ptr("mykey"),
				},
			},
		},
	}, nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	_, err = client.Update(ctx, "rg-1", "acctcmkpatch", armstorage.AccountUpdateParameters{
		Tags: map[string]*string{"env": to.Ptr("prod")},
	}, nil)
	require.NoError(t, err)

	got, err := client.GetProperties(ctx, "rg-1", "acctcmkpatch", nil)
	require.NoError(t, err)
	assertCMKEncryption(t, got.Properties)
	require.NotNil(t, got.Tags["env"])
	assert.Equal(t, "prod", *got.Tags["env"])
}
