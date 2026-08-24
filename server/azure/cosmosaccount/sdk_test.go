// Real-SDK round-trip test: the live azure-sdk-for-go armcosmos
// DatabaseAccountsClient drives the in-memory handler end-to-end, proving the
// database-account cost fields (kind, databaseAccountOfferType, enableFreeTier,
// capabilities) survive a create -> get.

package cosmosaccount_test

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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func newDatabaseAccountsClient(t *testing.T) *armcosmos.DatabaseAccountsClient {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{CosmosDB: cloudP.CosmosDB})

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

	client, err := armcosmos.NewDatabaseAccountsClient("sub-1", fakeCred{}, opts)
	require.NoError(t, err)

	return client
}

func TestSDKDatabaseAccountCreateGet(t *testing.T) {
	ctx := context.Background()
	client := newDatabaseAccountsClient(t)

	poller, err := client.BeginCreateOrUpdate(ctx, "rg-1", "cosmos1", armcosmos.DatabaseAccountCreateUpdateParameters{
		Location: to.Ptr("westus2"),
		Kind:     to.Ptr(armcosmos.DatabaseAccountKindMongoDB),
		Properties: &armcosmos.DatabaseAccountCreateUpdateProperties{
			DatabaseAccountOfferType: to.Ptr("Standard"),
			EnableFreeTier:           to.Ptr(true),
			Capabilities: []*armcosmos.Capability{
				{Name: to.Ptr("EnableServerless")},
			},
			Locations: []*armcosmos.Location{
				{LocationName: to.Ptr("westus2"), FailoverPriority: to.Ptr[int32](0)},
			},
		},
		Tags: map[string]*string{"env": to.Ptr("prod")},
	}, nil)
	require.NoError(t, err)

	created, err := poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	assertCosmosCostFields(t, created.Kind, created.Properties)
	require.NotNil(t, created.Name)
	assert.Equal(t, "cosmos1", *created.Name)

	// ... and survive an independent GET.
	got, err := client.Get(ctx, "rg-1", "cosmos1", nil)
	require.NoError(t, err)

	assertCosmosCostFields(t, got.Kind, got.Properties)
	require.NotNil(t, got.ID)
	assert.Contains(t, *got.ID, "/providers/Microsoft.DocumentDB/databaseAccounts/cosmos1")
}

func assertCosmosCostFields(t *testing.T, kind *armcosmos.DatabaseAccountKind, props *armcosmos.DatabaseAccountGetProperties) {
	t.Helper()

	require.NotNil(t, kind)
	assert.Equal(t, armcosmos.DatabaseAccountKindMongoDB, *kind)

	require.NotNil(t, props)
	require.NotNil(t, props.DatabaseAccountOfferType)
	assert.Equal(t, "Standard", *props.DatabaseAccountOfferType)
	require.NotNil(t, props.EnableFreeTier)
	assert.True(t, *props.EnableFreeTier)

	require.Len(t, props.Capabilities, 1)
	require.NotNil(t, props.Capabilities[0].Name)
	assert.Equal(t, "EnableServerless", *props.Capabilities[0].Name)
}

func TestSDKDatabaseAccountGetMissing(t *testing.T) {
	ctx := context.Background()
	client := newDatabaseAccountsClient(t)

	_, err := client.Get(ctx, "rg-1", "nope", nil)
	require.Error(t, err)
}

// createAccount is a helper that provisions an account and returns its client.
func createAccount(t *testing.T, client *armcosmos.DatabaseAccountsClient, rg, name, region string) {
	t.Helper()

	poller, err := client.BeginCreateOrUpdate(context.Background(), rg, name, armcosmos.DatabaseAccountCreateUpdateParameters{
		Location: to.Ptr(region),
		Kind:     to.Ptr(armcosmos.DatabaseAccountKindGlobalDocumentDB),
		Properties: &armcosmos.DatabaseAccountCreateUpdateProperties{
			DatabaseAccountOfferType: to.Ptr("Standard"),
			Locations: []*armcosmos.Location{
				{LocationName: to.Ptr(region), FailoverPriority: to.Ptr[int32](0)},
			},
		},
		Tags: map[string]*string{"team": to.Ptr("data")},
	}, nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(context.Background(), nil)
	require.NoError(t, err)
}

// TestSDKDatabaseAccountGetEndpointAndTags asserts the Get response carries the
// creation region, tags, the global documentEndpoint, and the read/write/
// location arrays a real account exposes.
func TestSDKDatabaseAccountGetEndpointAndTags(t *testing.T) {
	ctx := context.Background()
	client := newDatabaseAccountsClient(t)
	createAccount(t, client, "rg-1", "cosmos-ep", "westeurope")

	got, err := client.Get(ctx, "rg-1", "cosmos-ep", nil)
	require.NoError(t, err)

	require.NotNil(t, got.Location)
	assert.Equal(t, "westeurope", *got.Location)

	require.NotNil(t, got.Tags["team"])
	assert.Equal(t, "data", *got.Tags["team"])

	require.NotNil(t, got.Properties)
	require.NotNil(t, got.Properties.DocumentEndpoint)
	assert.Equal(t, "https://cosmos-ep.documents.azure.com:443/", *got.Properties.DocumentEndpoint)

	require.Len(t, got.Properties.WriteLocations, 1)
	require.NotNil(t, got.Properties.WriteLocations[0].LocationName)
	assert.Equal(t, "westeurope", *got.Properties.WriteLocations[0].LocationName)

	require.Len(t, got.Properties.ReadLocations, 1)
	require.Len(t, got.Properties.Locations, 1)
	require.Len(t, got.Properties.FailoverPolicies, 1)
}

// TestSDKDatabaseAccountListKeys drives ListKeys and the read-only keys.
func TestSDKDatabaseAccountListKeys(t *testing.T) {
	ctx := context.Background()
	client := newDatabaseAccountsClient(t)
	createAccount(t, client, "rg-1", "cosmos-keys", "eastus")

	keys, err := client.ListKeys(ctx, "rg-1", "cosmos-keys", nil)
	require.NoError(t, err)

	require.NotNil(t, keys.PrimaryMasterKey)
	require.NotNil(t, keys.SecondaryMasterKey)
	require.NotNil(t, keys.PrimaryReadonlyMasterKey)
	require.NotNil(t, keys.SecondaryReadonlyMasterKey)

	assert.NotEmpty(t, *keys.PrimaryMasterKey)
	assert.NotEqual(t, *keys.PrimaryMasterKey, *keys.SecondaryMasterKey)
	assert.NotEqual(t, *keys.PrimaryMasterKey, *keys.PrimaryReadonlyMasterKey)

	ro, err := client.ListReadOnlyKeys(ctx, "rg-1", "cosmos-keys", nil)
	require.NoError(t, err)
	require.NotNil(t, ro.PrimaryReadonlyMasterKey)
	assert.Equal(t, *keys.PrimaryReadonlyMasterKey, *ro.PrimaryReadonlyMasterKey)
}

// TestSDKDatabaseAccountListConnectionStrings drives ListConnectionStrings.
func TestSDKDatabaseAccountListConnectionStrings(t *testing.T) {
	ctx := context.Background()
	client := newDatabaseAccountsClient(t)
	createAccount(t, client, "rg-1", "cosmos-conn", "eastus")

	res, err := client.ListConnectionStrings(ctx, "rg-1", "cosmos-conn", nil)
	require.NoError(t, err)

	require.NotEmpty(t, res.ConnectionStrings)
	first := res.ConnectionStrings[0]
	require.NotNil(t, first.ConnectionString)
	assert.Contains(t, *first.ConnectionString, "AccountEndpoint=https://cosmos-conn.documents.azure.com:443/")
	require.NotNil(t, first.KeyKind)
	assert.Equal(t, armcosmos.KindPrimary, *first.KeyKind)
	require.NotNil(t, first.Type)
	assert.Equal(t, armcosmos.TypeSQL, *first.Type)
}

// TestSDKDatabaseAccountRegenerateKey drives the BeginRegenerateKey LRO.
func TestSDKDatabaseAccountRegenerateKey(t *testing.T) {
	ctx := context.Background()
	client := newDatabaseAccountsClient(t)
	createAccount(t, client, "rg-1", "cosmos-regen", "eastus")

	poller, err := client.BeginRegenerateKey(ctx, "rg-1", "cosmos-regen", armcosmos.DatabaseAccountRegenerateKeyParameters{
		KeyKind: to.Ptr(armcosmos.KeyKindPrimary),
	}, nil)
	require.NoError(t, err)

	_, err = poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)
}

// TestSDKDatabaseAccountList drives List (subscription) and ListByResourceGroup.
func TestSDKDatabaseAccountList(t *testing.T) {
	ctx := context.Background()
	client := newDatabaseAccountsClient(t)

	createAccount(t, client, "rg-a", "acct-a1", "eastus")
	createAccount(t, client, "rg-a", "acct-a2", "eastus")
	createAccount(t, client, "rg-b", "acct-b1", "westus")

	names := map[string]bool{}
	pager := client.NewListPager(nil)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		require.NoError(t, err)

		for _, acc := range page.Value {
			require.NotNil(t, acc.Name)
			names[*acc.Name] = true
		}
	}

	assert.True(t, names["acct-a1"] && names["acct-a2"] && names["acct-b1"],
		"subscription list should return all three accounts, got %v", names)

	// ListByResourceGroup filters to rg-a only.
	rgNames := map[string]bool{}
	rgPager := client.NewListByResourceGroupPager("rg-a", nil)

	for rgPager.More() {
		page, err := rgPager.NextPage(ctx)
		require.NoError(t, err)

		for _, acc := range page.Value {
			rgNames[*acc.Name] = true
		}
	}

	assert.True(t, rgNames["acct-a1"] && rgNames["acct-a2"], "rg-a list missing accounts: %v", rgNames)
	assert.False(t, rgNames["acct-b1"], "rg-a list must not include rg-b's account")
}
