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

// TestSDKResourceGraph_BugFixes pins the three issues called out in the
// PR #197 review so they cannot silently regress.
func TestSDKResourceGraph_BugFixes(t *testing.T) {
	ctx := context.Background()
	cloudP := cloudemu.NewAzure()

	require.NoError(t, cloudP.BlobStorage.CreateBucket(ctx, "bkt"))
	require.NoError(t, cloudP.BlobStorage.PutBucketTagging(ctx, "bkt",
		map[string]string{"env": "prod"}))

	_, err := cloudP.VNet.CreateVPC(ctx, netdriver.VPCConfig{
		CIDRBlock: "10.0.0.0/16", Tags: map[string]string{"env": "prod"},
	})
	require.NoError(t, err)

	t.Run("Bug 1: contradictory type filters return empty (AND, not OR)", func(t *testing.T) {
		srv := azureserver.New(azureserver.Drivers{
			BlobStorage:       cloudP.BlobStorage,
			Network:           cloudP.VNet,
			ResourceDiscovery: cloudP.ResourceDiscovery,
			SubscriptionID:    "123456789012",
		})
		ts := httptest.NewTLSServer(srv)
		t.Cleanup(ts.Close)

		client := newResourceGraphClient(t, ts)
		out, err := client.Resources(ctx, armresourcegraph.QueryRequest{
			Query: to.Ptr("Resources | where type == 'microsoft.compute/virtualmachines' | where type == 'microsoft.storage/storageaccounts'"),
		}, nil)
		require.NoError(t, err)
		assert.EqualValues(t, 0, *out.TotalRecords, "AND of two distinct types must yield zero")
	})

	t.Run("Bug 1: conflicting tag values for same key return empty", func(t *testing.T) {
		srv := azureserver.New(azureserver.Drivers{
			BlobStorage:       cloudP.BlobStorage,
			Network:           cloudP.VNet,
			ResourceDiscovery: cloudP.ResourceDiscovery,
			SubscriptionID:    "123456789012",
		})
		ts := httptest.NewTLSServer(srv)
		t.Cleanup(ts.Close)

		client := newResourceGraphClient(t, ts)
		out, err := client.Resources(ctx, armresourcegraph.QueryRequest{
			Query: to.Ptr("Resources | where tags['env'] == 'prod' | where tags['env'] == 'stage'"),
		}, nil)
		require.NoError(t, err)
		assert.EqualValues(t, 0, *out.TotalRecords)
	})

	t.Run("Bug 3: empty SubscriptionID falls back to engine.AccountID", func(t *testing.T) {
		// Wire the server WITHOUT explicit SubscriptionID. Scoped queries
		// for the engine's account ID should still work.
		srv := azureserver.New(azureserver.Drivers{
			BlobStorage:       cloudP.BlobStorage,
			Network:           cloudP.VNet,
			ResourceDiscovery: cloudP.ResourceDiscovery,
			// SubscriptionID intentionally omitted.
		})
		ts := httptest.NewTLSServer(srv)
		t.Cleanup(ts.Close)

		client := newResourceGraphClient(t, ts)
		out, err := client.Resources(ctx, armresourcegraph.QueryRequest{
			Query:         to.Ptr("Resources"),
			Subscriptions: []*string{to.Ptr(cloudP.ResourceDiscovery.AccountID())},
		}, nil)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, *out.TotalRecords, int64(1),
			"with SubscriptionID omitted, the handler must fall back to engine.AccountID() so scoped queries still match")
	})

	t.Run("Bug 2: /resources prefix does not shadow /resourcesHistory routing", func(t *testing.T) {
		// Smoke-test the shadow case via the real client: ResourcesHistory
		// must route to its own handler (which delegates back to
		// queryResources today, but the dispatch path is distinct).
		srv := azureserver.New(azureserver.Drivers{
			BlobStorage:       cloudP.BlobStorage,
			Network:           cloudP.VNet,
			ResourceDiscovery: cloudP.ResourceDiscovery,
			SubscriptionID:    "123456789012",
		})
		ts := httptest.NewTLSServer(srv)
		t.Cleanup(ts.Close)

		client := newResourceGraphClient(t, ts)
		out, err := client.ResourcesHistory(ctx, armresourcegraph.ResourcesHistoryRequest{
			Query: to.Ptr("Resources"),
		}, nil)
		require.NoError(t, err)
		require.NotNil(t, out)
	})
}
