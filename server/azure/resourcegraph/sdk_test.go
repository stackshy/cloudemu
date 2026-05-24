// Real-SDK round-trip test: the live azure-sdk-for-go
// armresourcegraph client drives the in-memory handler end-to-end.

package resourcegraph_test

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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu"
	dbdriver "github.com/stackshy/cloudemu/database/driver"
	netdriver "github.com/stackshy/cloudemu/networking/driver"
	azureserver "github.com/stackshy/cloudemu/server/azure"
)

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func TestSDKResourceGraph(t *testing.T) {
	ctx := context.Background()
	cloudP := cloudemu.NewAzure()

	require.NoError(t, cloudP.BlobStorage.CreateBucket(ctx, "audit-logs"))
	require.NoError(t, cloudP.BlobStorage.PutBucketTagging(ctx, "audit-logs",
		map[string]string{"env": "prod", "team": "security"}))

	require.NoError(t, cloudP.BlobStorage.CreateBucket(ctx, "stage-logs"))
	require.NoError(t, cloudP.BlobStorage.PutBucketTagging(ctx, "stage-logs",
		map[string]string{"env": "stage"}))

	require.NoError(t, cloudP.CosmosDB.CreateTable(ctx, dbdriver.TableConfig{Name: "events", PartitionKey: "pk"}))
	require.NoError(t, cloudP.CosmosDB.TagResource(ctx, "events", map[string]string{"env": "prod"}))

	_, err := cloudP.VNet.CreateVPC(ctx, netdriver.VPCConfig{
		CIDRBlock: "10.0.0.0/16", Tags: map[string]string{"env": "prod"},
	})
	require.NoError(t, err)

	srv := azureserver.New(azureserver.Drivers{
		BlobStorage:       cloudP.BlobStorage,
		CosmosDB:          cloudP.CosmosDB,
		Network:           cloudP.VNet,
		ResourceDiscovery: cloudP.ResourceDiscovery,
		SubscriptionID:    "123456789012",
	})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newResourceGraphClient(t, ts)

	t.Run("query all resources", func(t *testing.T) {
		out, err := client.Resources(ctx, armresourcegraph.QueryRequest{
			Query: to.Ptr("Resources"),
		}, nil)
		require.NoError(t, err)

		data, ok := out.Data.([]any)
		require.True(t, ok, "expected []any data, got %T", out.Data)
		assert.GreaterOrEqual(t, len(data), 4, "expect 2 buckets + 1 table + 1 vnet")
	})

	t.Run("filter by type", func(t *testing.T) {
		out, err := client.Resources(ctx, armresourcegraph.QueryRequest{
			Query: to.Ptr("Resources | where type == 'microsoft.storage/storageaccounts'"),
		}, nil)
		require.NoError(t, err)

		data := out.Data.([]any)
		assert.Len(t, data, 2, "two storage accounts")
	})

	t.Run("filter by type and tag", func(t *testing.T) {
		out, err := client.Resources(ctx, armresourcegraph.QueryRequest{
			Query: to.Ptr("Resources | where type == 'microsoft.storage/storageaccounts' | where tags['env'] == 'prod'"),
		}, nil)
		require.NoError(t, err)

		data := out.Data.([]any)
		assert.Len(t, data, 1)

		row := data[0].(map[string]any)
		assert.Equal(t, "audit-logs", row["name"])
		assert.Equal(t, "microsoft.storage/storageaccounts", row["type"])

		tags := row["tags"].(map[string]any)
		assert.Equal(t, "prod", tags["env"])
	})

	t.Run("case-insensitive type match", func(t *testing.T) {
		out, err := client.Resources(ctx, armresourcegraph.QueryRequest{
			Query: to.Ptr("Resources | where type =~ 'Microsoft.Network/VirtualNetworks'"),
		}, nil)
		require.NoError(t, err)

		data := out.Data.([]any)
		assert.Len(t, data, 1, "the one VNet we seeded")
	})

	t.Run("subscription scoping — wrong sub returns empty", func(t *testing.T) {
		out, err := client.Resources(ctx, armresourcegraph.QueryRequest{
			Query:         to.Ptr("Resources"),
			Subscriptions: []*string{to.Ptr("some-other-sub")},
		}, nil)
		require.NoError(t, err)
		assert.EqualValues(t, 0, *out.TotalRecords)
	})

	t.Run("limit clause caps results", func(t *testing.T) {
		out, err := client.Resources(ctx, armresourcegraph.QueryRequest{
			Query: to.Ptr("Resources | limit 1"),
		}, nil)
		require.NoError(t, err)

		data := out.Data.([]any)
		assert.Len(t, data, 1)
	})

	t.Run("$top option caps results", func(t *testing.T) {
		out, err := client.Resources(ctx, armresourcegraph.QueryRequest{
			Query: to.Ptr("Resources"),
			Options: &armresourcegraph.QueryRequestOptions{
				Top: to.Ptr(int32(2)),
			},
		}, nil)
		require.NoError(t, err)

		data := out.Data.([]any)
		assert.Len(t, data, 2)
	})

	t.Run("returned resource IDs are Azure-shaped", func(t *testing.T) {
		out, err := client.Resources(ctx, armresourcegraph.QueryRequest{
			Query: to.Ptr("Resources | where type == 'microsoft.network/virtualnetworks'"),
		}, nil)
		require.NoError(t, err)

		data := out.Data.([]any)
		require.Len(t, data, 1)

		row := data[0].(map[string]any)
		id := row["id"].(string)
		assert.Contains(t, id, "/subscriptions/123456789012/")
		assert.Contains(t, id, "Microsoft.Network")
		assert.Equal(t, "123456789012", row["subscriptionId"])
	})
}

func newResourceGraphClient(t *testing.T, ts *httptest.Server) *armresourcegraph.Client {
	t.Helper()

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

	cf, err := armresourcegraph.NewClientFactory(fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	return cf.NewClient()
}
