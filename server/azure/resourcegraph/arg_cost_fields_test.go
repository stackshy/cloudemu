// Real-SDK ARG round-trip tests for the cost-relevant fields that flow through
// Resource Graph across compute/network, managed-data, storage/database, and App
// Service plans. The live armresourcegraph client drives the in-memory handler
// end-to-end and asserts the sku/kind/properties slots an offline cost consumer
// prices on.
//
// Shared helpers (fakeCred, newResourceGraphClient) come from sdk_test.go in this
// same test package.
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
	"github.com/stackshy/cloudemu/v2/providers/azure/functions"
	"github.com/stackshy/cloudemu/v2/providers/azure/virtualmachines"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	dbxdriver "github.com/stackshy/cloudemu/v2/services/databricks/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// argCostClient wires an Azure Resource Graph handler over the provider's
// discovery engine and returns a real armresourcegraph client pointed at it.
// It supplies every driver the cost-field tests need — the blob/cosmos drivers
// for the storage tests and ResourceDiscovery for everything else.
func argCostClient(t *testing.T, cloudP *azureprovider.Provider) *armresourcegraph.Client {
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

// queryOne runs a `Resources | where type =~ '<typ>'` query through the real
// client and requires exactly one decoded row, which it returns.
func queryOne(t *testing.T, client *armresourcegraph.Client, typ string) map[string]any {
	t.Helper()

	out, err := client.Resources(context.Background(), armresourcegraph.QueryRequest{
		Query: to.Ptr("Resources | where type =~ '" + typ +
			"' | project id,name,type,location,resourceGroup,kind,properties,sku,zones,tags"),
	}, nil)
	require.NoError(t, err)

	data, ok := out.Data.([]any)
	require.True(t, ok, "expected []any data, got %T", out.Data)
	require.Len(t, data, 1, "expected exactly one %s row", typ)

	row, ok := data[0].(map[string]any)
	require.True(t, ok, "expected map row, got %T", data[0])

	return row
}

// rowObj asserts the value at key is a JSON object and returns it.
func rowObj(t *testing.T, parent map[string]any, key string) map[string]any {
	t.Helper()

	v, ok := parent[key]
	require.True(t, ok, "missing key %q in %v", key, parent)

	m, ok := v.(map[string]any)
	require.True(t, ok, "key %q is %T, want object", key, v)

	return m
}

func rowProps(t *testing.T, row map[string]any) map[string]any {
	t.Helper()

	return rowObj(t, row, "properties")
}

func rowSKU(t *testing.T, row map[string]any) map[string]any {
	t.Helper()

	return rowObj(t, row, "sku")
}

// TestARGCostFields_Compute pins the newly-added cost/discovery fields for
// compute and network resources so they cannot silently regress.
func TestARGCostFields_Compute(t *testing.T) {
	ctx := context.Background()
	cloudP := cloudemu.NewAzure()

	// 1. Managed disk with provisioned performance and a tier.
	_, err := cloudP.VirtualMachines.CreateVolume(ctx, computedriver.VolumeConfig{
		Size: 4, VolumeType: "PremiumV2_LRS", IOPS: 5000, Throughput: 200, Tier: "P10",
	})
	require.NoError(t, err)

	// 2. VM with priority / license / OS / zone.
	_, err = cloudP.VirtualMachines.RunInstances(ctx, computedriver.InstanceConfig{
		InstanceType: "Standard_D2s_v3", OSType: "Linux",
		Priority: "Spot", LicenseType: "Windows_Server", Zones: []string{"1"},
	}, 1)
	require.NoError(t, err)

	// 3. Public IP with the Azure defaults (Standard SKU, Static allocation).
	_, err = cloudP.VNet.AllocateAddress(ctx, netdriver.ElasticIPConfig{})
	require.NoError(t, err)

	// 4. VNet + subnet carrying address prefixes.
	vpc, err := cloudP.VNet.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	_, err = cloudP.VNet.CreateSubnet(ctx, netdriver.SubnetConfig{
		VPCID: vpc.ID, CIDRBlock: "10.0.1.0/24",
	})
	require.NoError(t, err)

	// 5. VM Scale Set with SKU + per-VM profile.
	_, err = cloudP.VirtualMachines.CreateScaleSet(ctx, virtualmachines.ScaleSet{
		Name: "vmss1", SKUName: "Standard_D4s_v3", Capacity: 5,
		Priority: "Spot", LicenseType: "Windows_Server", OSType: "Linux",
	})
	require.NoError(t, err)

	client := argCostClient(t, cloudP)

	t.Run("disk", func(t *testing.T) {
		row := queryOne(t, client, "microsoft.compute/disks")
		props := rowProps(t, row)
		sku := rowSKU(t, row)

		// JSON numbers decode as float64 — assert with EqualValues.
		assert.EqualValues(t, 5000, props["diskIOPSReadWrite"])
		assert.EqualValues(t, 200, props["diskMBpsReadWrite"])
		assert.Equal(t, "P10", props["tier"])
		assert.Equal(t, "PremiumV2_LRS", sku["name"])
		assert.Equal(t, "P10", sku["tier"])
	})

	t.Run("vm", func(t *testing.T) {
		row := queryOne(t, client, "microsoft.compute/virtualmachines")
		props := rowProps(t, row)
		sku := rowSKU(t, row)

		assert.Equal(t, "Spot", props["priority"])
		assert.Equal(t, "Windows_Server", props["licenseType"])
		assert.Equal(t, "Linux", props["osType"])
		assert.Equal(t, "Standard_D2s_v3", sku["name"])

		zones, ok := row["zones"].([]any)
		require.True(t, ok, "vm row has no zones array: %v", row["zones"])
		assert.Contains(t, zones, "1")
	})

	t.Run("publicip", func(t *testing.T) {
		row := queryOne(t, client, "microsoft.network/publicipaddresses")

		// The type itself proves the fixed portable->Azure type map.
		assert.Equal(t, "microsoft.network/publicipaddresses", row["type"])
		assert.Equal(t, "Standard", rowSKU(t, row)["name"])
		assert.Equal(t, "Static", rowProps(t, row)["publicIPAllocationMethod"])
	})

	t.Run("vnet", func(t *testing.T) {
		row := queryOne(t, client, "microsoft.network/virtualnetworks")
		props := rowProps(t, row)

		addrSpace, ok := props["addressSpace"].(map[string]any)
		require.True(t, ok, "vnet has no addressSpace: %v", props)

		prefixes, ok := addrSpace["addressPrefixes"].([]any)
		require.True(t, ok, "addressSpace has no addressPrefixes array: %v", addrSpace)
		assert.Contains(t, prefixes, "10.0.0.0/16")
	})

	t.Run("subnet", func(t *testing.T) {
		row := queryOne(t, client, "microsoft.network/subnets")
		assert.Equal(t, "10.0.1.0/24", rowProps(t, row)["addressPrefix"])
	})

	t.Run("vmss", func(t *testing.T) {
		row := queryOne(t, client, "microsoft.compute/virtualmachinescalesets")
		sku := rowSKU(t, row)

		assert.Equal(t, "Standard_D4s_v3", sku["name"])
		assert.EqualValues(t, 5, sku["capacity"])

		profile, ok := rowProps(t, row)["virtualMachineProfile"].(map[string]any)
		require.True(t, ok, "vmss has no virtualMachineProfile: %v", row["properties"])
		assert.Equal(t, "Spot", profile["priority"])
		assert.Equal(t, "Windows_Server", profile["licenseType"])

		storageProfile, ok := profile["storageProfile"].(map[string]any)
		require.True(t, ok, "profile has no storageProfile: %v", profile)

		osDisk, ok := storageProfile["osDisk"].(map[string]any)
		require.True(t, ok, "storageProfile has no osDisk: %v", storageProfile)
		assert.Equal(t, "Linux", osDisk["osType"])
	})
}

// TestARGCostFields_ManagedInstance pins the SQL Managed Instance cost inputs:
// the backup storage redundancy (storageAccountType) and the provisioned storage
// size must round-trip through Resource Graph properties.
func TestARGCostFields_ManagedInstance(t *testing.T) {
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

	client := argCostClient(t, cloudP)

	row := queryOne(t, client, "microsoft.sql/managedinstances")
	assert.Equal(t, "mi-cost", row["name"])

	props := rowProps(t, row)
	assert.Equal(t, "ZoneRedundant", props["storageAccountType"])
	assert.EqualValues(t, 256, props["storageSizeInGB"])
}

// TestARGCostFields_MySQLFlex proves the MySQL Flexible Server projects its
// compute SKU (with a derived Burstable tier), engine version, storage size and
// HA mode through the same generic sku/properties slots.
func TestARGCostFields_MySQLFlex(t *testing.T) {
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

	client := argCostClient(t, cloudP)

	row := queryOne(t, client, "microsoft.dbformysql/flexibleservers")

	sku := rowSKU(t, row)
	assert.Equal(t, "Standard_B1ms", sku["name"])
	assert.Equal(t, "Burstable", sku["tier"], "tier is derived from the SKU family prefix")

	props := rowProps(t, row)
	assert.Equal(t, "8.0.21", props["version"])
	assert.EqualValues(t, 64, rowObj(t, props, "storage")["storageSizeGB"])
	assert.Equal(t, "ZoneRedundant", rowObj(t, props, "highAvailability")["mode"])
}

// TestARGCostFields_AKS pins the AKS cost inputs across the cluster (uptime-SLA
// tier, power state, kubernetes version) and its Spot agent pool
// (scaleSetPriority, node count, vmSize).
func TestARGCostFields_AKS(t *testing.T) {
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

	client := argCostClient(t, cloudP)

	t.Run("cluster row carries tier, powerState and kubernetesVersion", func(t *testing.T) {
		row := queryOne(t, client, "microsoft.containerservice/managedclusters")
		assert.Equal(t, "aks-cost", row["name"])
		assert.Equal(t, "Standard", rowSKU(t, row)["tier"])

		props := rowProps(t, row)
		assert.Equal(t, "Running", rowObj(t, props, "powerState")["code"])
		assert.NotEmpty(t, props["kubernetesVersion"], "kubernetes version must be set")
	})

	t.Run("agent pool row carries Spot priority, count and vmSize", func(t *testing.T) {
		row := queryOne(t, client, "microsoft.containerservice/managedclusters/agentpools")
		assert.Equal(t, "Standard_DS2_v2", rowSKU(t, row)["name"])

		props := rowProps(t, row)
		assert.Equal(t, "Spot", props["scaleSetPriority"])
		assert.EqualValues(t, 2, props["count"])
		assert.Equal(t, "User", props["mode"], "inline pools default to the User mode")
		assert.Equal(t, "Linux", props["osType"], "inline pools default to the Linux osType")
	})
}

// TestARGCostFields_Databricks asserts the Databricks workspace SKU (the pricing
// tier) round-trips, along with the provisioning state / workspace id the mock
// populates on create.
func TestARGCostFields_Databricks(t *testing.T) {
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

	client := argCostClient(t, cloudP)

	row := queryOne(t, client, "microsoft.databricks/workspaces")
	assert.Equal(t, "dbx-cost", row["name"])

	sku := rowSKU(t, row)
	assert.Equal(t, "premium", sku["name"])

	if tier, ok := sku["tier"]; ok {
		assert.Equal(t, "premium", tier)
	}

	// provisioningState and workspaceId are populated by the mock on create, so
	// the properties bag must surface them for a cost/discovery consumer.
	props := rowProps(t, row)
	assert.NotEmpty(t, props["provisioningState"])
	assert.NotEmpty(t, props["workspaceId"])
}

// TestARGCostFields_SQLDatabase proves a logical SQL database created on a SQL
// server surfaces through Resource Graph with the cost-relevant SKU and
// zone-redundancy fields. The sqlDiscovery adapter enumerates databases per
// server by calling ListDatabases(server) where server == the SQL server's ID,
// so the database's Server must match the seeded server's ID to be discovered.
func TestARGCostFields_SQLDatabase(t *testing.T) {
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

	client := argCostClient(t, cloudP)

	row := queryOne(t, client, "microsoft.sql/servers/databases")
	assert.Equal(t, "appdb", row["name"])

	sku := rowSKU(t, row)
	assert.Equal(t, "GP_Gen5_4", sku["name"])

	props := rowProps(t, row)
	zr, ok := props["zoneRedundant"].(bool)
	require.True(t, ok, "zoneRedundant is %T, want bool", props["zoneRedundant"])
	assert.True(t, zr)

	currentSku := rowObj(t, props, "currentSku")
	assert.Equal(t, "GP_Gen5_4", currentSku["name"])
	assert.Equal(t, "GeneralPurpose", currentSku["tier"])
}

// TestARGCostFields_StorageAccount proves a blob container's seeded
// storage-account attributes (SKU redundancy, kind, access tier) round-trip
// through Resource Graph as the top-level kind, sku.name, and
// properties.accessTier a cost consumer prices on.
func TestARGCostFields_StorageAccount(t *testing.T) {
	ctx := context.Background()
	cloudP := cloudemu.NewAzure()

	require.NoError(t, cloudP.BlobStorage.CreateBucket(ctx, "mybucket"))
	cloudP.BlobStorage.SetBucketAttributes("mybucket", storagedriver.AccountAttributes{
		SKU:        "Premium_LRS",
		Kind:       "BlockBlobStorage",
		AccessTier: "Hot",
	})

	client := argCostClient(t, cloudP)

	row := queryOne(t, client, "microsoft.storage/storageaccounts")
	assert.Equal(t, "mybucket", row["name"])
	assert.Equal(t, "BlockBlobStorage", row["kind"])
	assert.Equal(t, "Premium_LRS", rowSKU(t, row)["name"])

	props := rowProps(t, row)
	assert.Equal(t, "Hot", props["accessTier"])
}

// TestARGCostFields_CosmosAccount proves a Cosmos container's seeded account
// attributes (kind, offer type, capabilities, free-tier flag) round-trip through
// Resource Graph as the top-level kind and the
// properties.databaseAccountOfferType / capabilities[].name / enableFreeTier
// fields a cost consumer prices on.
func TestARGCostFields_CosmosAccount(t *testing.T) {
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

	client := argCostClient(t, cloudP)

	row := queryOne(t, client, "microsoft.documentdb/databaseaccounts")
	assert.Equal(t, "events", row["name"])
	assert.Equal(t, "GlobalDocumentDB", row["kind"])

	props := rowProps(t, row)
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

// TestARGCostFields_AppServicePlan pins the App Service plan cost inputs: the
// SKU name/tier/capacity and the plan kind must round-trip through Resource
// Graph as microsoft.web/serverfarms.
func TestARGCostFields_AppServicePlan(t *testing.T) {
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

	client := argCostClient(t, cloudP)

	row := queryOne(t, client, "microsoft.web/serverfarms")
	assert.Equal(t, "plan1", row["name"])
	assert.Equal(t, "microsoft.web/serverfarms", row["type"])
	assert.Equal(t, "linux", row["kind"])

	sku := rowSKU(t, row)
	assert.Equal(t, "P1v3", sku["name"])
	assert.Equal(t, "PremiumV3", sku["tier"])
	// JSON numbers decode as float64 through the any-typed row.
	assert.EqualValues(t, 3, sku["capacity"])
}
