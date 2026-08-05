// Real-SDK ARG round-trip for the Azure App Service plan (Microsoft.Web/
// serverfarms) discovery added in this PR. The live armresourcegraph client
// drives the in-memory handler end-to-end and asserts the sku/kind slots an
// offline cost consumer prices an App Service / Function App plan on.
//
// Shared helpers (fakeCred, newResourceGraphClient) are reused from sdk_test.go
// in this same test package; local helpers here are prefixed argPlan... to stay
// collision-free with sibling test files.
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
	"github.com/stackshy/cloudemu/v2/providers/azure/functions"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestSDKResourceGraph_AppServicePlanCostData pins the App Service plan cost
// inputs: the SKU name/tier/capacity and the plan kind must round-trip through
// Resource Graph as microsoft.web/serverfarms.
func TestSDKResourceGraph_AppServicePlanCostData(t *testing.T) {
	ctx := context.Background()
	cloudP := cloudemu.NewAzure()

	_, err := cloudP.Functions.CreateAppServicePlan(ctx, functions.AppServicePlan{
		Name:     "plan1",
		SKUName:  "P1v3",
		SKUTier:  "PremiumV3",
		Kind:     "linux",
		Capacity: 3,
	})
	require.NoError(t, err)

	client := argPlanNewClient(t, cloudP)

	out, err := client.Resources(ctx, armresourcegraph.QueryRequest{
		Query: to.Ptr("Resources | where type =~ 'microsoft.web/serverfarms' | project id,name,type,sku,kind"),
	}, nil)
	require.NoError(t, err)

	argPlanData, ok := out.Data.([]any)
	require.True(t, ok, "expected []any data, got %T", out.Data)
	require.Len(t, argPlanData, 1)

	argPlanRow := argPlanData[0].(map[string]any)
	assert.Equal(t, "plan1", argPlanRow["name"])
	assert.Equal(t, "microsoft.web/serverfarms", argPlanRow["type"])
	assert.Equal(t, "linux", argPlanRow["kind"])

	argPlanSKU, ok := argPlanRow["sku"].(map[string]any)
	require.True(t, ok, "sku is %T, want object", argPlanRow["sku"])
	assert.Equal(t, "P1v3", argPlanSKU["name"])
	assert.Equal(t, "PremiumV3", argPlanSKU["tier"])
	// JSON numbers decode as float64 through the any-typed row.
	assert.EqualValues(t, 3, argPlanSKU["capacity"])
}

// argPlanNewClient wires an Azure Resource Graph handler over the provider's
// discovery engine and returns a real armresourcegraph client pointed at it.
// App Service plans surface through the shared discovery engine, so the handler
// needs only ResourceDiscovery + SubscriptionID.
func argPlanNewClient(t *testing.T, cloudP *azureprovider.Provider) *armresourcegraph.Client {
	t.Helper()

	srv := azureserver.New(azureserver.Drivers{
		ResourceDiscovery: cloudP.ResourceDiscovery,
		SubscriptionID:    "123456789012",
	})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	return newResourceGraphClient(t, ts)
}
