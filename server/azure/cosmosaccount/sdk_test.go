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
	"github.com/stackshy/cloudemu/v2/server"
	"github.com/stackshy/cloudemu/v2/server/azure/cosmosaccount"
)

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func newDatabaseAccountsClient(t *testing.T) *armcosmos.DatabaseAccountsClient {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := server.New(cosmosaccount.New(cloudP.CosmosDB))

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
