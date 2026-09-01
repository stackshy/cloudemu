// Real-SDK round-trip test for issue #913: a compute VM opted in via the
// cloudemu:sqlvm=true tag surfaces a paired Microsoft.SqlVirtualMachine overlay
// row in Resource Graph, while a plain VM does not.

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
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// TestSDKResourceGraph_SQLVirtualMachineDiscovery pins issue #913: the SQL VM
// overlay is a management view on a compute VM. A VM tagged cloudemu:sqlvm=true
// gets one microsoft.sqlvirtualmachine/sqlvirtualmachines row that shares the
// VM's name, resource group, location and (user-visible) tags — differing only
// in the provider segment of its id — and an untagged VM gets none.
func TestSDKResourceGraph_SQLVirtualMachineDiscovery(t *testing.T) {
	ctx := context.Background()
	cloudP := cloudemu.NewAzure()

	sqlVM, err := cloudP.VirtualMachines.RunInstances(ctx, computedriver.InstanceConfig{
		InstanceType:  "Standard_D2s_v3",
		Region:        "eastus",
		ResourceGroup: "sql-rg",
		Tags:          map[string]string{"cloudemu:sqlvm": "true", "env": "prod"},
	}, 1)
	require.NoError(t, err)
	require.Len(t, sqlVM, 1)

	plainVM, err := cloudP.VirtualMachines.RunInstances(ctx, computedriver.InstanceConfig{
		InstanceType:  "Standard_D2s_v3",
		Region:        "eastus",
		ResourceGroup: "plain-rg",
		Tags:          map[string]string{"env": "dev"},
	}, 1)
	require.NoError(t, err)
	require.Len(t, plainVM, 1)

	srv := azureserver.New(azureserver.Drivers{
		ResourceDiscovery: cloudP.ResourceDiscovery,
		SubscriptionID:    "123456789012",
	})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newResourceGraphClient(t, ts)

	t.Run("exactly one SQL VM overlay row, paired to the tagged VM", func(t *testing.T) {
		out, err := client.Resources(ctx, armresourcegraph.QueryRequest{
			Query: to.Ptr("Resources | where type =~ 'microsoft.sqlvirtualmachine/sqlvirtualmachines'"),
		}, nil)
		require.NoError(t, err)

		data := out.Data.([]any)
		require.Len(t, data, 1, "only the tagged VM produces an overlay row")

		row := data[0].(map[string]any)
		assert.Equal(t, "microsoft.sqlvirtualmachine/sqlvirtualmachines", row["type"])
		assert.Equal(t, sqlVM[0].ID, row["name"], "overlay shares the paired VM's name")
		assert.Equal(t, "sql-rg", row["resourceGroup"], "overlay shares the VM's resource group")
		assert.Equal(t, "eastus", row["location"], "overlay shares the VM's location")

		// Only the provider segment differs from the compute VM's id; the internal
		// cloudemu: opt-in tag is stripped on the wire, leaving the real user tags.
		id := row["id"].(string)
		assert.Contains(t, id, "/subscriptions/123456789012/")
		assert.Contains(t, id, "resourceGroups/sql-rg/")
		assert.Contains(t, id, "/providers/Microsoft.SqlVirtualMachine/sqlVirtualMachines/"+sqlVM[0].ID)

		tags := row["tags"].(map[string]any)
		assert.Equal(t, "prod", tags["env"])
		assert.NotContains(t, tags, "cloudemu:sqlvm", "internal opt-in marker must not leak")
	})

	t.Run("the paired compute VM row still appears with the same name", func(t *testing.T) {
		out, err := client.Resources(ctx, armresourcegraph.QueryRequest{
			Query: to.Ptr("Resources | where type =~ 'microsoft.compute/virtualmachines'"),
		}, nil)
		require.NoError(t, err)

		data := out.Data.([]any)
		require.Len(t, data, 2, "both VMs remain plain compute rows")
		assert.True(t, rowsHaveType(data, "microsoft.compute/virtualmachines"))
	})

	t.Run("a plain untagged VM yields no SQL VM overlay row", func(t *testing.T) {
		out, err := client.Resources(ctx, armresourcegraph.QueryRequest{
			Query: to.Ptr("Resources | where type =~ 'microsoft.sqlvirtualmachine/sqlvirtualmachines'"),
		}, nil)
		require.NoError(t, err)

		data := out.Data.([]any)
		for _, d := range data {
			row := d.(map[string]any)
			assert.NotEqual(t, plainVM[0].ID, row["name"], "the plain VM must not surface an overlay")
		}
	})
}
