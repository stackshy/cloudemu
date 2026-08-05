// Real-SDK round-trip tests for the cost-relevant compute/network fields that
// flow through Resource Graph: managed-disk performance (IOPS/MBps/tier), VM
// priority/license/OS, public-IP SKU + allocation method, VNet/subnet address
// prefixes, and VMSS sku + virtualMachineProfile. The live armresourcegraph
// client drives the in-memory handler end-to-end so a real discovery + cost
// consumer sees exactly these row shapes.

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
	"github.com/stackshy/cloudemu/v2/providers/azure/virtualmachines"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// argComputeQueryRows runs a KQL query through the real client and returns the
// result rows as a []map[string]any.
func argComputeQueryRows(ctx context.Context, t *testing.T, c *armresourcegraph.Client, kql string) []map[string]any {
	t.Helper()

	out, err := c.Resources(ctx, armresourcegraph.QueryRequest{Query: to.Ptr(kql)}, nil)
	require.NoError(t, err)

	data, ok := out.Data.([]any)
	require.True(t, ok, "expected []any data, got %T", out.Data)

	rows := make([]map[string]any, 0, len(data))
	for _, d := range data {
		row, ok := d.(map[string]any)
		require.True(t, ok, "expected map row, got %T", d)
		rows = append(rows, row)
	}

	return rows
}

// argComputeOneRow queries by exact Azure type and requires exactly one row.
func argComputeOneRow(ctx context.Context, t *testing.T, c *armresourcegraph.Client, azureType string) map[string]any {
	t.Helper()

	rows := argComputeQueryRows(ctx, t, c,
		"Resources | where type =~ '"+azureType+
			"' | project id,name,type,location,properties,sku,zones,tags")
	require.Len(t, rows, 1, "expected exactly one %s row", azureType)

	return rows[0]
}

func argComputeProps(t *testing.T, row map[string]any) map[string]any {
	t.Helper()

	props, ok := row["properties"].(map[string]any)
	require.True(t, ok, "row %v has no properties object", row)

	return props
}

func argComputeSKU(t *testing.T, row map[string]any) map[string]any {
	t.Helper()

	sku, ok := row["sku"].(map[string]any)
	require.True(t, ok, "row %v has no sku object", row)

	return sku
}

// TestSDKResourceGraph_CostComputeFields pins the newly-added cost/discovery
// fields for compute and network resources so they cannot silently regress.
func TestSDKResourceGraph_CostComputeFields(t *testing.T) {
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

	srv := azureserver.New(azureserver.Drivers{
		ResourceDiscovery: cloudP.ResourceDiscovery,
		SubscriptionID:    "123456789012",
	})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newResourceGraphClient(t, ts)

	t.Run("disk", func(t *testing.T) {
		row := argComputeOneRow(ctx, t, client, "microsoft.compute/disks")
		props := argComputeProps(t, row)
		sku := argComputeSKU(t, row)

		// JSON numbers decode as float64 — assert with EqualValues.
		assert.EqualValues(t, 5000, props["diskIOPSReadWrite"])
		assert.EqualValues(t, 200, props["diskMBpsReadWrite"])
		assert.Equal(t, "P10", props["tier"])
		assert.Equal(t, "PremiumV2_LRS", sku["name"])
		assert.Equal(t, "P10", sku["tier"])
	})

	t.Run("vm", func(t *testing.T) {
		row := argComputeOneRow(ctx, t, client, "microsoft.compute/virtualmachines")
		props := argComputeProps(t, row)
		sku := argComputeSKU(t, row)

		assert.Equal(t, "Spot", props["priority"])
		assert.Equal(t, "Windows_Server", props["licenseType"])
		assert.Equal(t, "Linux", props["osType"])
		assert.Equal(t, "Standard_D2s_v3", sku["name"])

		zones, ok := row["zones"].([]any)
		require.True(t, ok, "vm row has no zones array: %v", row["zones"])
		assert.Contains(t, zones, "1")
	})

	t.Run("publicip", func(t *testing.T) {
		row := argComputeOneRow(ctx, t, client, "microsoft.network/publicipaddresses")

		// The type itself proves the fixed portable->Azure type map.
		assert.Equal(t, "microsoft.network/publicipaddresses", row["type"])
		assert.Equal(t, "Standard", argComputeSKU(t, row)["name"])
		assert.Equal(t, "Static", argComputeProps(t, row)["publicIPAllocationMethod"])
	})

	t.Run("vnet", func(t *testing.T) {
		row := argComputeOneRow(ctx, t, client, "microsoft.network/virtualnetworks")
		props := argComputeProps(t, row)

		addrSpace, ok := props["addressSpace"].(map[string]any)
		require.True(t, ok, "vnet has no addressSpace: %v", props)

		prefixes, ok := addrSpace["addressPrefixes"].([]any)
		require.True(t, ok, "addressSpace has no addressPrefixes array: %v", addrSpace)
		assert.Contains(t, prefixes, "10.0.0.0/16")
	})

	t.Run("subnet", func(t *testing.T) {
		row := argComputeOneRow(ctx, t, client, "microsoft.network/subnets")
		assert.Equal(t, "10.0.1.0/24", argComputeProps(t, row)["addressPrefix"])
	})

	t.Run("vmss", func(t *testing.T) {
		row := argComputeOneRow(ctx, t, client, "microsoft.compute/virtualmachinescalesets")
		sku := argComputeSKU(t, row)

		assert.Equal(t, "Standard_D4s_v3", sku["name"])
		assert.EqualValues(t, 5, sku["capacity"])

		profile, ok := argComputeProps(t, row)["virtualMachineProfile"].(map[string]any)
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
