// Real-SDK round-trip tests for the cost-relevant ARG fields added to the
// managed-data services (SQL Managed Instance, MySQL Flexible Server, AKS
// clusters + agent pools, Databricks workspaces). The live armresourcegraph
// client drives the in-memory handler end-to-end and asserts the sku/properties
// slots an offline cost consumer prices on.
//
// Shared helpers (fakeCred, newResourceGraphClient, findRowByType, rowsHaveType)
// are reused from sdk_test.go in this same test package; local helpers here are
// prefixed argData... to stay collision-free with sibling test files.
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
	"github.com/stackshy/cloudemu/v2/providers/azure/aks"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	dbxdriver "github.com/stackshy/cloudemu/v2/services/databricks/driver"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// argDataQueryType runs a `Resources | where type =~ '<typ>'` query through the
// real client and returns the decoded rows.
func argDataQueryType(t *testing.T, client *armresourcegraph.Client, typ string) []any {
	t.Helper()

	out, err := client.Resources(context.Background(), armresourcegraph.QueryRequest{
		Query: to.Ptr("Resources | where type =~ '" + typ +
			"' | project id,name,type,location,resourceGroup,properties,sku,tags"),
	}, nil)
	require.NoError(t, err)

	data, ok := out.Data.([]any)
	require.True(t, ok, "expected []any data, got %T", out.Data)

	return data
}

// argDataMap asserts the value at key is a JSON object and returns it.
func argDataMap(t *testing.T, parent map[string]any, key string) map[string]any {
	t.Helper()

	v, ok := parent[key]
	require.True(t, ok, "missing key %q in %v", key, parent)

	m, ok := v.(map[string]any)
	require.True(t, ok, "key %q is %T, want object", key, v)

	return m
}

// TestSDKResourceGraph_ManagedInstanceCostData pins the SQL Managed Instance
// cost inputs: the backup storage redundancy (storageAccountType) and the
// provisioned storage size must round-trip through Resource Graph properties.
func TestSDKResourceGraph_ManagedInstanceCostData(t *testing.T) {
	ctx := context.Background()
	cloudP := cloudemu.NewAzure()

	_, err := cloudP.SQL.CreateManagedInstance(ctx, rdsdriver.ManagedInstanceConfig{
		Name:               "mi-cost",
		SubnetID:           "/subscriptions/123456789012/resourceGroups/rg-1/providers/Microsoft.Network/virtualNetworks/vn/subnets/mi",
		VCores:             8,
		StorageGB:          256,
		StorageAccountType: "ZoneRedundant",
	})
	require.NoError(t, err)

	client := argDataNewClient(t, cloudP)

	data := argDataQueryType(t, client, "microsoft.sql/managedinstances")
	require.Len(t, data, 1)

	row := data[0].(map[string]any)
	assert.Equal(t, "mi-cost", row["name"])

	props := argDataMap(t, row, "properties")
	assert.Equal(t, "ZoneRedundant", props["storageAccountType"])
	assert.EqualValues(t, 256, props["storageSizeInGB"])
}

// TestSDKResourceGraph_MySQLFlexCostData proves the MySQL Flexible Server projects
// its compute SKU (with a derived Burstable tier), engine version, storage size
// and HA mode through the same generic sku/properties slots.
func TestSDKResourceGraph_MySQLFlexCostData(t *testing.T) {
	ctx := context.Background()
	cloudP := cloudemu.NewAzure()

	_, err := cloudP.MySQLFlex.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID:               "mysql-cost",
		InstanceClass:    "Standard_B1ms",
		EngineVersion:    "8.0.21",
		AllocatedStorage: 64,
		MultiAZ:          true,
	})
	require.NoError(t, err)

	client := argDataNewClient(t, cloudP)

	data := argDataQueryType(t, client, "microsoft.dbformysql/flexibleservers")
	require.Len(t, data, 1)

	row := data[0].(map[string]any)

	sku := argDataMap(t, row, "sku")
	assert.Equal(t, "Standard_B1ms", sku["name"])
	assert.Equal(t, "Burstable", sku["tier"], "tier is derived from the SKU family prefix")

	props := argDataMap(t, row, "properties")
	assert.Equal(t, "8.0.21", props["version"])
	assert.EqualValues(t, 64, argDataMap(t, props, "storage")["storageSizeGB"])
	assert.Equal(t, "ZoneRedundant", argDataMap(t, props, "highAvailability")["mode"])
}

// TestSDKResourceGraph_AKSCostData pins the AKS cost inputs across the cluster
// (uptime-SLA tier, power state, kubernetes version) and its Spot agent pool
// (scaleSetPriority, node count, vmSize).
func TestSDKResourceGraph_AKSCostData(t *testing.T) {
	ctx := context.Background()
	cloudP := cloudemu.NewAzure()

	_, err := cloudP.AKS.CreateOrUpdateCluster(ctx, aks.ClusterInput{
		ResourceGroup: "rg-1",
		Name:          "aks-cost",
		Location:      "eastus",
		Tier:          "Standard",
		AgentPools: []aks.AgentPoolInput{
			{
				Name:             "spotpool",
				Count:            2,
				VMSize:           "Standard_DS2_v2",
				ScaleSetPriority: "Spot",
			},
		},
	})
	require.NoError(t, err)

	client := argDataNewClient(t, cloudP)

	t.Run("cluster row carries tier, powerState and kubernetesVersion", func(t *testing.T) {
		data := argDataQueryType(t, client, "microsoft.containerservice/managedclusters")
		require.Len(t, data, 1)

		row := data[0].(map[string]any)
		assert.Equal(t, "aks-cost", row["name"])
		assert.Equal(t, "Standard", argDataMap(t, row, "sku")["tier"])

		props := argDataMap(t, row, "properties")
		assert.Equal(t, "Running", argDataMap(t, props, "powerState")["code"])
		assert.NotEmpty(t, props["kubernetesVersion"], "kubernetes version must be set")
	})

	t.Run("agent pool row carries Spot priority, count and vmSize", func(t *testing.T) {
		data := argDataQueryType(t, client, "microsoft.containerservice/managedclusters/agentpools")
		require.Len(t, data, 1)

		row := data[0].(map[string]any)
		assert.Equal(t, "Standard_DS2_v2", argDataMap(t, row, "sku")["name"])

		props := argDataMap(t, row, "properties")
		assert.Equal(t, "Spot", props["scaleSetPriority"])
		assert.EqualValues(t, 2, props["count"])
		assert.Equal(t, "User", props["mode"], "inline pools default to the User mode")
		assert.Equal(t, "Linux", props["osType"], "inline pools default to the Linux osType")
	})
}

// TestSDKResourceGraph_DatabricksCostData asserts the Databricks workspace SKU
// (the pricing tier) round-trips, along with the provisioning state / workspace
// id the mock populates on create.
func TestSDKResourceGraph_DatabricksCostData(t *testing.T) {
	ctx := context.Background()
	cloudP := cloudemu.NewAzure()

	_, err := cloudP.Databricks.CreateWorkspace(ctx, dbxdriver.WorkspaceConfig{
		Name:                   "dbx-cost",
		ResourceGroup:          "rg-1",
		Location:               "eastus",
		SKUName:                "premium",
		SKUTier:                "premium",
		ManagedResourceGroupID: "/subscriptions/123456789012/resourceGroups/databricks-rg-1",
	})
	require.NoError(t, err)

	client := argDataNewClient(t, cloudP)

	data := argDataQueryType(t, client, "microsoft.databricks/workspaces")
	require.Len(t, data, 1)

	row := data[0].(map[string]any)
	assert.Equal(t, "dbx-cost", row["name"])

	sku := argDataMap(t, row, "sku")
	assert.Equal(t, "premium", sku["name"])

	if tier, ok := sku["tier"]; ok {
		assert.Equal(t, "premium", tier)
	}

	// provisioningState and workspaceId are populated by the mock on create, so
	// the properties bag must surface them for a cost/discovery consumer.
	props := argDataMap(t, row, "properties")
	assert.NotEmpty(t, props["provisioningState"])
	assert.NotEmpty(t, props["workspaceId"])
}

// TestSDKResourceGraph_SQLDatabaseCostData proves a logical SQL database created
// on a SQL server surfaces through Resource Graph with the cost-relevant SKU and
// zone-redundancy fields. The sqlDiscovery adapter enumerates databases per
// server by calling ListDatabases(server) where server == the SQL server's ID,
// so the database's Server must match the seeded server's ID to be discovered.
func TestSDKResourceGraph_SQLDatabaseCostData(t *testing.T) {
	ctx := context.Background()
	cloudP := cloudemu.NewAzure()

	server, err := cloudP.SQL.CreateCluster(ctx, rdsdriver.ClusterConfig{
		ID:             "sql-cost",
		MasterUsername: "admin",
		EngineVersion:  "12.0",
	})
	require.NoError(t, err)

	_, err = cloudP.SQL.CreateDatabase(ctx, rdsdriver.DatabaseConfig{
		Server:        server.ID,
		Name:          "appdb",
		SKUName:       "GP_Gen5_4",
		SKUTier:       "GeneralPurpose",
		ZoneRedundant: true,
	})
	require.NoError(t, err)

	client := argDataNewClient(t, cloudP)

	data := argDataQueryType(t, client, "microsoft.sql/servers/databases")
	require.Len(t, data, 1)

	row := data[0].(map[string]any)
	assert.Equal(t, "appdb", row["name"])

	sku := argDataMap(t, row, "sku")
	assert.Equal(t, "GP_Gen5_4", sku["name"])

	props := argDataMap(t, row, "properties")
	zr, ok := props["zoneRedundant"].(bool)
	require.True(t, ok, "zoneRedundant is %T, want bool", props["zoneRedundant"])
	assert.True(t, zr)

	currentSku := argDataMap(t, props, "currentSku")
	assert.Equal(t, "GP_Gen5_4", currentSku["name"])
	assert.Equal(t, "GeneralPurpose", currentSku["tier"])
}

// argDataNewClient wires an Azure Resource Graph handler over the provider's
// discovery engine and returns a real armresourcegraph client pointed at it.
// The handler reads only ResourceDiscovery + SubscriptionID; all managed-data
// resources surface through the shared discovery engine.
func argDataNewClient(t *testing.T, cloudP *azureprovider.Provider) *armresourcegraph.Client {
	t.Helper()

	srv := azureserver.New(azureserver.Drivers{
		ResourceDiscovery: cloudP.ResourceDiscovery,
		SubscriptionID:    "123456789012",
	})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	return newResourceGraphClient(t, ts)
}
