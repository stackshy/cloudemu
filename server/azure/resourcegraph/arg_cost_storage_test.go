// Real-SDK round-trip tests for the optional storage/database discovery
// capabilities: an Azure blob container projects its storage-account SKU / kind
// / access tier, and a Cosmos container projects its account kind / offer type /
// capabilities / free-tier flag. The live armresourcegraph client drives the
// in-memory handler end-to-end and asserts the sku/kind/properties slots an
// offline cost consumer prices on.
//
// Shared helpers (fakeCred, newResourceGraphClient) are reused from sdk_test.go
// in this same test package; local helpers here are prefixed argStore... to stay
// collision-free with the sibling argData*/argCompute* test files.
package resourcegraph_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	azureprovider "github.com/stackshy/cloudemu/v2/providers/azure"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// argStoreQueryType runs a `Resources | where type =~ '<typ>'` query through the
// real client and returns the decoded rows.
func argStoreQueryType(t *testing.T, client *armresourcegraph.Client, typ string) []any {
	t.Helper()

	out, err := client.Resources(context.Background(), armresourcegraph.QueryRequest{
		Query: to.Ptr("Resources | where type =~ '" + typ +
			"' | project id,name,type,location,resourceGroup,kind,properties,sku,tags"),
	}, nil)
	require.NoError(t, err)

	data, ok := out.Data.([]any)
	require.True(t, ok, "expected []any data, got %T", out.Data)

	return data
}

// argStoreMap asserts the value at key is a JSON object and returns it.
func argStoreMap(t *testing.T, parent map[string]any, key string) map[string]any {
	t.Helper()

	v, ok := parent[key]
	require.True(t, ok, "missing key %q in %v", key, parent)

	m, ok := v.(map[string]any)
	require.True(t, ok, "key %q is %T, want object", key, v)

	return m
}

// argStoreNewClient wires an Azure Resource Graph handler over the provider's
// discovery engine and returns a real armresourcegraph client pointed at it.
func argStoreNewClient(t *testing.T, cloudP *azureprovider.Provider) *armresourcegraph.Client {
	t.Helper()

	srv := azureserver.New(azureserver.Drivers{
		BlobStorage:       cloudP.BlobStorage,
		CosmosDB:          cloudP.CosmosDB,
		ResourceDiscovery: cloudP.ResourceDiscovery,
		SubscriptionID:    "123456789012",
	})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	return newResourceGraphClient(t, ts)
}

// TestSDKResourceGraph_StorageAccountCostData proves a blob container's seeded
// storage-account attributes (SKU redundancy, kind, access tier) round-trip
// through Resource Graph as the top-level kind, sku.name, and
// properties.accessTier a cost consumer prices on.
func TestSDKResourceGraph_StorageAccountCostData(t *testing.T) {
	ctx := context.Background()
	cloudP := cloudemu.NewAzure()

	require.NoError(t, cloudP.BlobStorage.CreateBucket(ctx, "mybucket"))
	cloudP.BlobStorage.SetBucketAttributes("mybucket", storagedriver.AccountAttributes{
		SKU:        "Premium_LRS",
		Kind:       "BlockBlobStorage",
		AccessTier: "Hot",
	})

	client := argStoreNewClient(t, cloudP)

	data := argStoreQueryType(t, client, "microsoft.storage/storageaccounts")
	require.Len(t, data, 1)

	row := data[0].(map[string]any)
	assert.Equal(t, "mybucket", row["name"])
	assert.Equal(t, "BlockBlobStorage", row["kind"])
	assert.Equal(t, "Premium_LRS", argStoreMap(t, row, "sku")["name"])

	props := argStoreMap(t, row, "properties")
	assert.Equal(t, "Hot", props["accessTier"])
}

// TestSDKResourceGraph_CosmosAccountCostData proves a Cosmos container's seeded
// account attributes (kind, offer type, capabilities, free-tier flag) round-trip
// through Resource Graph as the top-level kind and the
// properties.databaseAccountOfferType / capabilities[].name / enableFreeTier
// fields a cost consumer prices on.
func TestSDKResourceGraph_CosmosAccountCostData(t *testing.T) {
	ctx := context.Background()
	cloudP := cloudemu.NewAzure()

	require.NoError(t, cloudP.CosmosDB.CreateTable(ctx, dbdriver.TableConfig{
		Name: "events", PartitionKey: "pk",
	}))
	cloudP.CosmosDB.SetTableAttributes("events", dbdriver.AccountAttributes{
		Kind:           "GlobalDocumentDB",
		OfferType:      "Standard",
		EnableFreeTier: true,
		Capabilities:   []string{"EnableServerless"},
	})

	client := argStoreNewClient(t, cloudP)

	data := argStoreQueryType(t, client, "microsoft.documentdb/databaseaccounts")
	require.Len(t, data, 1)

	row := data[0].(map[string]any)
	assert.Equal(t, "events", row["name"])
	assert.Equal(t, "GlobalDocumentDB", row["kind"])

	props := argStoreMap(t, row, "properties")
	assert.Equal(t, "Standard", props["databaseAccountOfferType"])

	freeTier, ok := props["enableFreeTier"].(bool)
	require.True(t, ok, "enableFreeTier is %T, want bool", props["enableFreeTier"])
	assert.True(t, freeTier)

	caps, ok := props["capabilities"].([]any)
	require.True(t, ok, "capabilities is %T, want []any", props["capabilities"])
	require.Len(t, caps, 1)

	firstCap, ok := caps[0].(map[string]any)
	require.True(t, ok, "capabilities[0] is %T, want object", caps[0])
	assert.Equal(t, "EnableServerless", firstCap["name"])
}
