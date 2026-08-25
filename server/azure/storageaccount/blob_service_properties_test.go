// Real-SDK round-trip tests: the live azure-sdk-for-go armstorage
// BlobServicesClient drives the in-memory handler end-to-end, proving the
// blobServices/default sub-resource is a distinct resource from the storage
// account itself (it must never fall through to the account create/update
// handler and wipe the account's SKU/properties), and that Set/Get Blob
// Service Properties round-trips versioning, change feed, soft delete, and
// CORS.

package storageaccount_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	azureprovider "github.com/stackshy/cloudemu/v2/providers/azure"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

func newBlobServicesClient(t *testing.T, cloudP *azureprovider.Provider) *armstorage.BlobServicesClient {
	t.Helper()

	srv := azureserver.New(azureserver.Drivers{BlobStorage: cloudP.BlobStorage})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Endpoint: ts.URL,
				Audience: "https://management.azure.com",
			},
		},
	}

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	client, err := armstorage.NewBlobServicesClient("sub-1", fakeCred{}, opts)
	require.NoError(t, err)

	return client
}

// TestSDKSetServicePropertiesDoesNotWipeAccount is the regression test for the
// blocker bug: a PUT to blobServices/default (BlobServicesClient.
// SetServiceProperties — the call the SDK/CLI use to enable blob versioning,
// soft delete, CORS, or the change feed) was misrouted into the account
// create/update handler and silently reset the account's SKU/kind/tags to
// their zero-value defaults.
func TestSDKSetServicePropertiesDoesNotWipeAccount(t *testing.T) {
	ctx := context.Background()
	// newAccountsClient/newBlobServicesClient each spin up their own TLS
	// server; share the same underlying blob-storage provider so both point at
	// the same account state.
	cloudP := cloudemu.NewAzure()
	accounts := newAccountsClientFor(t, cloudP)
	blobServices := newBlobServicesClient(t, cloudP)

	poller, err := accounts.BeginCreate(ctx, "rg-1", "acctblob", armstorage.AccountCreateParameters{
		Location: to.Ptr("eastus"),
		Kind:     to.Ptr(armstorage.KindStorageV2),
		SKU:      &armstorage.SKU{Name: to.Ptr(armstorage.SKUNamePremiumLRS)},
		Tags:     map[string]*string{"env": to.Ptr("prod")},
	}, nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	_, err = blobServices.SetServiceProperties(ctx, "rg-1", "acctblob", armstorage.BlobServiceProperties{
		BlobServiceProperties: &armstorage.BlobServicePropertiesProperties{
			IsVersioningEnabled: to.Ptr(true),
			ChangeFeed:          &armstorage.ChangeFeed{Enabled: to.Ptr(true)},
			DeleteRetentionPolicy: &armstorage.DeleteRetentionPolicy{
				Enabled: to.Ptr(true), Days: to.Ptr[int32](14),
			},
		},
	}, nil)
	require.NoError(t, err)

	got, err := accounts.GetProperties(ctx, "rg-1", "acctblob", nil)
	require.NoError(t, err)

	require.NotNil(t, got.SKU, "SetServiceProperties must not wipe the account's SKU")
	assert.Equal(t, armstorage.SKUNamePremiumLRS, *got.SKU.Name)
	require.NotNil(t, got.Kind)
	assert.Equal(t, armstorage.KindStorageV2, *got.Kind)
	require.NotNil(t, got.Tags["env"])
	assert.Equal(t, "prod", *got.Tags["env"])
}

// TestSDKBlobServicePropertiesRoundTrip proves versioning/change-feed/soft-delete/
// CORS submitted via SetServiceProperties survive an independent
// GetServiceProperties call.
func TestSDKBlobServicePropertiesRoundTrip(t *testing.T) {
	ctx := context.Background()
	cloudP := cloudemu.NewAzure()
	accounts := newAccountsClientFor(t, cloudP)
	blobServices := newBlobServicesClient(t, cloudP)

	poller, err := accounts.BeginCreate(ctx, "rg-1", "acctblob2", armstorage.AccountCreateParameters{
		Location: to.Ptr("eastus"),
		Kind:     to.Ptr(armstorage.KindStorageV2),
		SKU:      &armstorage.SKU{Name: to.Ptr(armstorage.SKUNameStandardLRS)},
	}, nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	set, err := blobServices.SetServiceProperties(ctx, "rg-1", "acctblob2", armstorage.BlobServiceProperties{
		BlobServiceProperties: &armstorage.BlobServicePropertiesProperties{
			IsVersioningEnabled: to.Ptr(true),
			ChangeFeed:          &armstorage.ChangeFeed{Enabled: to.Ptr(true), RetentionInDays: to.Ptr[int32](30)},
			DeleteRetentionPolicy: &armstorage.DeleteRetentionPolicy{
				Enabled: to.Ptr(true), Days: to.Ptr[int32](7),
			},
			Cors: &armstorage.CorsRules{
				CorsRules: []*armstorage.CorsRule{{
					AllowedOrigins:  []*string{to.Ptr("*")},
					AllowedMethods:  []*armstorage.CorsRuleAllowedMethodsItem{to.Ptr(armstorage.CorsRuleAllowedMethodsItemGET)},
					AllowedHeaders:  []*string{to.Ptr("*")},
					ExposedHeaders:  []*string{to.Ptr("*")},
					MaxAgeInSeconds: to.Ptr[int32](3600),
				}},
			},
		},
	}, nil)
	require.NoError(t, err)
	assertBlobServiceProps(t, set.BlobServiceProperties)

	got, err := blobServices.GetServiceProperties(ctx, "rg-1", "acctblob2", nil)
	require.NoError(t, err)
	assertBlobServiceProps(t, got.BlobServiceProperties)
	assert.Contains(t, *got.ID, "/blobServices/default")
}

func assertBlobServiceProps(t *testing.T, props armstorage.BlobServiceProperties) {
	t.Helper()

	require.NotNil(t, props.BlobServiceProperties)
	p := props.BlobServiceProperties

	require.NotNil(t, p.IsVersioningEnabled)
	assert.True(t, *p.IsVersioningEnabled)

	require.NotNil(t, p.ChangeFeed)
	require.NotNil(t, p.ChangeFeed.Enabled)
	assert.True(t, *p.ChangeFeed.Enabled)
	require.NotNil(t, p.ChangeFeed.RetentionInDays)
	assert.EqualValues(t, 30, *p.ChangeFeed.RetentionInDays)

	require.NotNil(t, p.DeleteRetentionPolicy)
	require.NotNil(t, p.DeleteRetentionPolicy.Enabled)
	assert.True(t, *p.DeleteRetentionPolicy.Enabled)
	require.NotNil(t, p.DeleteRetentionPolicy.Days)
	assert.EqualValues(t, 7, *p.DeleteRetentionPolicy.Days)

	require.NotNil(t, p.Cors)
	require.Len(t, p.Cors.CorsRules, 1)
	assert.Equal(t, []*string{to.Ptr("*")}, p.Cors.CorsRules[0].AllowedOrigins)
}

// TestSDKGetServicePropertiesMissingAccount proves the sub-resource 404s like
// real Azure when the parent storage account doesn't exist.
func TestSDKGetServicePropertiesMissingAccount(t *testing.T) {
	ctx := context.Background()
	cloudP := cloudemu.NewAzure()
	blobServices := newBlobServicesClient(t, cloudP)

	_, err := blobServices.GetServiceProperties(ctx, "rg-1", "nope", nil)
	require.Error(t, err)
}
