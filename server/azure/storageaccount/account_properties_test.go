package storageaccount_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSDKStorageAccountLocationAndTags proves the submitted region and tags
// survive an independent GET (previously the account always reported eastus and
// dropped its tags).
func TestSDKStorageAccountLocationAndTags(t *testing.T) {
	ctx := context.Background()
	client := newAccountsClient(t)

	poller, err := client.BeginCreate(ctx, "rg-1", "acct1", armstorage.AccountCreateParameters{
		Location: to.Ptr("westus2"),
		Kind:     to.Ptr(armstorage.KindStorageV2),
		SKU:      &armstorage.SKU{Name: to.Ptr(armstorage.SKUNameStandardLRS)},
		Tags:     map[string]*string{"env": to.Ptr("prod"), "team": to.Ptr("platform")},
	}, nil)
	require.NoError(t, err)

	_, err = poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	got, err := client.GetProperties(ctx, "rg-1", "acct1", nil)
	require.NoError(t, err)

	require.NotNil(t, got.Location)
	assert.Equal(t, "westus2", *got.Location)

	require.NotNil(t, got.Tags["env"])
	assert.Equal(t, "prod", *got.Tags["env"])
	require.NotNil(t, got.Tags["team"])
	assert.Equal(t, "platform", *got.Tags["team"])
}

// TestSDKStorageAccountPropertiesCompleteness proves the GET response is
// populated with the endpoints/location/encryption fields SDK consumers expect.
func TestSDKStorageAccountPropertiesCompleteness(t *testing.T) {
	ctx := context.Background()
	client := newAccountsClient(t)

	poller, err := client.BeginCreate(ctx, "rg-1", "acct1", armstorage.AccountCreateParameters{
		Location: to.Ptr("westeurope"),
		Kind:     to.Ptr(armstorage.KindStorageV2),
		SKU:      &armstorage.SKU{Name: to.Ptr(armstorage.SKUNameStandardLRS)},
	}, nil)
	require.NoError(t, err)

	_, err = poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	got, err := client.GetProperties(ctx, "rg-1", "acct1", nil)
	require.NoError(t, err)

	props := got.Properties
	require.NotNil(t, props)

	require.NotNil(t, props.PrimaryLocation)
	assert.Equal(t, "westeurope", *props.PrimaryLocation)

	require.NotNil(t, props.StatusOfPrimary)
	assert.Equal(t, armstorage.AccountStatusAvailable, *props.StatusOfPrimary)

	require.NotNil(t, props.CreationTime)

	require.NotNil(t, props.PrimaryEndpoints)
	require.NotNil(t, props.PrimaryEndpoints.Blob)
	assert.True(t, strings.Contains(*props.PrimaryEndpoints.Blob, "acct1.blob.core.windows.net"),
		"blob endpoint = %q", *props.PrimaryEndpoints.Blob)
	require.NotNil(t, props.PrimaryEndpoints.Queue)
	require.NotNil(t, props.PrimaryEndpoints.Table)
	require.NotNil(t, props.PrimaryEndpoints.File)

	require.NotNil(t, props.Encryption)
	require.NotNil(t, props.Encryption.KeySource)
	assert.Equal(t, armstorage.KeySourceMicrosoftStorage, *props.Encryption.KeySource)
}
