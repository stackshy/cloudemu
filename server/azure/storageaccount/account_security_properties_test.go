package storageaccount_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSDKStorageAccountSecurityDefaults proves an account created WITHOUT the
// security toggles reads back the real-Azure defaults (minimum TLS 1.2, public
// network access Enabled, HTTPS-only true, blob public access false, shared-key
// access true). Before the fix these all came back nil, so a raw SDK/CLI read
// (az storage account show) or a Terraform refresh saw missing attributes.
func TestSDKStorageAccountSecurityDefaults(t *testing.T) {
	ctx := context.Background()
	client := newAccountsClient(t)

	poller, err := client.BeginCreate(ctx, "rg-1", "acctdef", armstorage.AccountCreateParameters{
		Location: to.Ptr("westus2"),
		Kind:     to.Ptr(armstorage.KindStorageV2),
		SKU:      &armstorage.SKU{Name: to.Ptr(armstorage.SKUNameStandardLRS)},
	}, nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	got, err := client.GetProperties(ctx, "rg-1", "acctdef", nil)
	require.NoError(t, err)
	props := got.Properties
	require.NotNil(t, props)

	require.NotNil(t, props.MinimumTLSVersion)
	assert.Equal(t, armstorage.MinimumTLSVersionTLS12, *props.MinimumTLSVersion)
	require.NotNil(t, props.PublicNetworkAccess)
	assert.Equal(t, armstorage.PublicNetworkAccessEnabled, *props.PublicNetworkAccess)
	require.NotNil(t, props.EnableHTTPSTrafficOnly)
	assert.True(t, *props.EnableHTTPSTrafficOnly, "supportsHttpsTrafficOnly defaults to true")
	require.NotNil(t, props.AllowBlobPublicAccess)
	assert.False(t, *props.AllowBlobPublicAccess, "allowBlobPublicAccess defaults to false")
	require.NotNil(t, props.AllowSharedKeyAccess)
	assert.True(t, *props.AllowSharedKeyAccess, "allowSharedKeyAccess defaults to true")
}

// TestSDKStorageAccountSecurityExplicitFalse proves the explicitly-false
// security booleans survive create -> get. The echo-properties overlay drops
// zero-valued scalars it doesn't model, so a Terraform user setting
// https_traffic_only_enabled/shared_access_key_enabled/allow_nested_items_to_be_public
// to false previously saw perpetual drift (the value read back as nil/absent).
func TestSDKStorageAccountSecurityExplicitFalse(t *testing.T) {
	ctx := context.Background()
	client := newAccountsClient(t)

	poller, err := client.BeginCreate(ctx, "rg-1", "acctsec", armstorage.AccountCreateParameters{
		Location: to.Ptr("westus2"),
		Kind:     to.Ptr(armstorage.KindStorageV2),
		SKU:      &armstorage.SKU{Name: to.Ptr(armstorage.SKUNameStandardLRS)},
		Properties: &armstorage.AccountPropertiesCreateParameters{
			EnableHTTPSTrafficOnly: to.Ptr(false),
			AllowBlobPublicAccess:  to.Ptr(false),
			AllowSharedKeyAccess:   to.Ptr(false),
			MinimumTLSVersion:      to.Ptr(armstorage.MinimumTLSVersionTLS10),
			PublicNetworkAccess:    to.Ptr(armstorage.PublicNetworkAccessDisabled),
		},
	}, nil)
	require.NoError(t, err)

	created, err := poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)
	assertSecurity(t, created.Properties, false, false, false,
		armstorage.MinimumTLSVersionTLS10, armstorage.PublicNetworkAccessDisabled)

	got, err := client.GetProperties(ctx, "rg-1", "acctsec", nil)
	require.NoError(t, err)
	assertSecurity(t, got.Properties, false, false, false,
		armstorage.MinimumTLSVersionTLS10, armstorage.PublicNetworkAccessDisabled)
}

// TestSDKStorageAccountSecurityPatchPreserve proves a PATCH that only changes
// tags leaves the security toggles untouched (partial-update semantics), and a
// PATCH that flips one toggle changes only that one.
func TestSDKStorageAccountSecurityPatchPreserve(t *testing.T) {
	ctx := context.Background()
	client := newAccountsClient(t)

	poller, err := client.BeginCreate(ctx, "rg-1", "acctpatch", armstorage.AccountCreateParameters{
		Location: to.Ptr("westus2"),
		SKU:      &armstorage.SKU{Name: to.Ptr(armstorage.SKUNameStandardLRS)},
		Properties: &armstorage.AccountPropertiesCreateParameters{
			EnableHTTPSTrafficOnly: to.Ptr(false),
			AllowSharedKeyAccess:   to.Ptr(false),
		},
	}, nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	_, err = client.Update(ctx, "rg-1", "acctpatch", armstorage.AccountUpdateParameters{
		Tags: map[string]*string{"env": to.Ptr("prod")},
	}, nil)
	require.NoError(t, err)

	got, err := client.GetProperties(ctx, "rg-1", "acctpatch", nil)
	require.NoError(t, err)
	require.NotNil(t, got.Properties.EnableHTTPSTrafficOnly)
	assert.False(t, *got.Properties.EnableHTTPSTrafficOnly, "tags-only PATCH must not reset httpsOnly")
	require.NotNil(t, got.Properties.AllowSharedKeyAccess)
	assert.False(t, *got.Properties.AllowSharedKeyAccess, "tags-only PATCH must not reset sharedKey")

	_, err = client.Update(ctx, "rg-1", "acctpatch", armstorage.AccountUpdateParameters{
		Properties: &armstorage.AccountPropertiesUpdateParameters{
			EnableHTTPSTrafficOnly: to.Ptr(true),
		},
	}, nil)
	require.NoError(t, err)

	got2, err := client.GetProperties(ctx, "rg-1", "acctpatch", nil)
	require.NoError(t, err)
	require.NotNil(t, got2.Properties.EnableHTTPSTrafficOnly)
	assert.True(t, *got2.Properties.EnableHTTPSTrafficOnly, "PATCH must flip httpsOnly to true")
	require.NotNil(t, got2.Properties.AllowSharedKeyAccess)
	assert.False(t, *got2.Properties.AllowSharedKeyAccess, "PATCH of one toggle must not touch the other")
}

// TestSDKStorageAccountSecurityReplaceResetsDefaults proves a re-PUT that omits
// a previously-set toggle resets it to the real-Azure default (create-or-update
// is full-replace, mirroring how sku/kind/tags already behave on this handler).
func TestSDKStorageAccountSecurityReplaceResetsDefaults(t *testing.T) {
	ctx := context.Background()
	client := newAccountsClient(t)

	create := func(props *armstorage.AccountPropertiesCreateParameters) {
		poller, err := client.BeginCreate(ctx, "rg-1", "acctrep", armstorage.AccountCreateParameters{
			Location:   to.Ptr("westus2"),
			SKU:        &armstorage.SKU{Name: to.Ptr(armstorage.SKUNameStandardLRS)},
			Properties: props,
		}, nil)
		require.NoError(t, err)
		_, err = poller.PollUntilDone(ctx, nil)
		require.NoError(t, err)
	}

	create(&armstorage.AccountPropertiesCreateParameters{EnableHTTPSTrafficOnly: to.Ptr(false)})
	create(nil) // re-PUT without the toggle

	got, err := client.GetProperties(ctx, "rg-1", "acctrep", nil)
	require.NoError(t, err)
	require.NotNil(t, got.Properties.EnableHTTPSTrafficOnly)
	assert.True(t, *got.Properties.EnableHTTPSTrafficOnly, "re-PUT omitting the toggle resets to default true")
}

func assertSecurity(
	t *testing.T, props *armstorage.AccountProperties,
	httpsOnly, blobPublic, sharedKey bool,
	tls armstorage.MinimumTLSVersion, pna armstorage.PublicNetworkAccess,
) {
	t.Helper()
	require.NotNil(t, props)
	require.NotNil(t, props.EnableHTTPSTrafficOnly)
	assert.Equal(t, httpsOnly, *props.EnableHTTPSTrafficOnly)
	require.NotNil(t, props.AllowBlobPublicAccess)
	assert.Equal(t, blobPublic, *props.AllowBlobPublicAccess)
	require.NotNil(t, props.AllowSharedKeyAccess)
	assert.Equal(t, sharedKey, *props.AllowSharedKeyAccess)
	require.NotNil(t, props.MinimumTLSVersion)
	assert.Equal(t, tls, *props.MinimumTLSVersion)
	require.NotNil(t, props.PublicNetworkAccess)
	assert.Equal(t, pna, *props.PublicNetworkAccess)
}
