// Real-SDK round-trip test: the live azure-sdk-for-go armstorage
// AccountsClient drives the in-memory handler end-to-end, proving the
// storage-account cost fields (sku.name, kind, properties.accessTier) survive a
// create -> get.

package storageaccount_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

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

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func newAccountsClient(t *testing.T) *armstorage.AccountsClient {
	t.Helper()

	return newAccountsClientFor(t, cloudemu.NewAzure())
}

// newAccountsClientFor builds an AccountsClient against a caller-supplied
// provider, so a test can share one cloudemu.Azure (and its blob-storage
// state) across an AccountsClient and a BlobServicesClient on independent TLS
// servers/connections.
func newAccountsClientFor(t *testing.T, cloudP *azureprovider.Provider) *armstorage.AccountsClient {
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

	client, err := armstorage.NewAccountsClient("sub-1", fakeCred{}, opts)
	require.NoError(t, err)

	return client
}

func TestSDKStorageAccountCreateGet(t *testing.T) {
	ctx := context.Background()
	client := newAccountsClient(t)

	poller, err := client.BeginCreate(ctx, "rg-1", "acct1", armstorage.AccountCreateParameters{
		Location: to.Ptr("westus2"),
		Kind:     to.Ptr(armstorage.KindStorageV2),
		SKU:      &armstorage.SKU{Name: to.Ptr(armstorage.SKUNamePremiumLRS)},
		Properties: &armstorage.AccountPropertiesCreateParameters{
			AccessTier: to.Ptr(armstorage.AccessTierCool),
		},
		Tags: map[string]*string{"env": to.Ptr("prod")},
	}, nil)
	require.NoError(t, err)

	created, err := poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	// Cost fields survive the create response.
	require.NotNil(t, created.SKU)
	assert.Equal(t, armstorage.SKUNamePremiumLRS, *created.SKU.Name)
	require.NotNil(t, created.Kind)
	assert.Equal(t, armstorage.KindStorageV2, *created.Kind)
	require.NotNil(t, created.Properties)
	require.NotNil(t, created.Properties.AccessTier)
	assert.Equal(t, armstorage.AccessTierCool, *created.Properties.AccessTier)
	assert.Equal(t, "acct1", *created.Name)

	// ... and survive an independent GET.
	got, err := client.GetProperties(ctx, "rg-1", "acct1", nil)
	require.NoError(t, err)

	require.NotNil(t, got.SKU)
	assert.Equal(t, armstorage.SKUNamePremiumLRS, *got.SKU.Name)
	require.NotNil(t, got.Kind)
	assert.Equal(t, armstorage.KindStorageV2, *got.Kind)
	require.NotNil(t, got.Properties)
	require.NotNil(t, got.Properties.AccessTier)
	assert.Equal(t, armstorage.AccessTierCool, *got.Properties.AccessTier)
	assert.Contains(t, *got.ID, "/providers/Microsoft.Storage/storageAccounts/acct1")
}

func TestSDKStorageAccountGetMissing(t *testing.T) {
	ctx := context.Background()
	client := newAccountsClient(t)

	_, err := client.GetProperties(ctx, "rg-1", "nope", nil)
	require.Error(t, err)
}

// A management-API create must be visible to the list calls, at both the
// subscription and resource-group scope — the gap reported in #404, where a
// PUT-created account was returned by GET-by-id but not by list.
func TestSDKStorageAccountList(t *testing.T) {
	ctx := context.Background()
	client := newAccountsClient(t)

	for _, name := range []string{"acctA", "acctB"} {
		poller, err := client.BeginCreate(ctx, "rg-1", name, armstorage.AccountCreateParameters{
			Location: to.Ptr("eastus"),
			Kind:     to.Ptr(armstorage.KindBlockBlobStorage),
			SKU:      &armstorage.SKU{Name: to.Ptr(armstorage.SKUNamePremiumLRS)},
		}, nil)
		require.NoError(t, err)
		_, err = poller.PollUntilDone(ctx, nil)
		require.NoError(t, err)
	}

	collect := func(pager interface {
		More() bool
	}, next func() ([]string, error)) []string {
		var names []string
		for pager.More() {
			page, err := next()
			require.NoError(t, err)
			names = append(names, page...)
		}
		return names
	}

	// Subscription-scoped list.
	subPager := client.NewListPager(nil)
	subNames := collect(subPager, func() ([]string, error) {
		page, err := subPager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(page.Value))
		for _, a := range page.Value {
			out = append(out, *a.Name)
		}
		return out, nil
	})
	assert.ElementsMatch(t, []string{"acctA", "acctB"}, subNames, "subscription-scoped list must return both accounts")

	// Resource-group-scoped list.
	rgPager := client.NewListByResourceGroupPager("rg-1", nil)
	rgNames := collect(rgPager, func() ([]string, error) {
		page, err := rgPager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(page.Value))
		for _, a := range page.Value {
			out = append(out, *a.Name)
		}
		return out, nil
	})
	assert.ElementsMatch(t, []string{"acctA", "acctB"}, rgNames, "resource-group-scoped list must return both accounts")
}
