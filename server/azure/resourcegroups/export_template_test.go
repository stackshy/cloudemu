// Regression test for exportTemplate actually enumerating a resource group's
// members: https://learn.microsoft.com/en-us/rest/api/resources/resource-groups/export-template
// documents a "resources" array of type/apiVersion/name/location/properties
// entries in the returned template — before this fix the handler always
// returned an empty array regardless of what the group held.

package resourcegroups_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// TestExportTemplateEnumeratesGroupResources drives the real armresources SDK
// end-to-end: a VM is created directly against the provider's compute driver
// (as the wire VM-create handler would leave it), the containing resource
// group is created through the wire API, and exportTemplate must report the
// VM in its resources[] rather than the empty skeleton it used to return.
func TestExportTemplateEnumeratesGroupResources(t *testing.T) {
	ctx := context.Background()
	cloudP := cloudemu.NewAzure()

	inst, err := cloudP.VirtualMachines.RunInstances(ctx, computedriver.InstanceConfig{
		ResourceGroup: "myrg",
		InstanceType:  "Standard_D2s_v3",
		OSType:        "Linux",
		Region:        "eastus",
	}, 1)
	require.NoError(t, err)
	require.Len(t, inst, 1)

	srv := azureserver.New(azureserver.Drivers{
		ResourceDiscovery: cloudP.ResourceDiscovery,
		SubscriptionID:    cloudP.SubscriptionID,
	})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	rgClient := newRGClient(t, ts)

	_, err = rgClient.CreateOrUpdate(ctx, "myrg", armresources.ResourceGroup{
		Location: to.Ptr("eastus"),
	}, nil)
	require.NoError(t, err)

	poller, err := rgClient.BeginExportTemplate(ctx, "myrg", armresources.ExportTemplateRequest{
		Resources: []*string{to.Ptr("*")},
	}, nil)
	require.NoError(t, err)

	result, err := poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	tmpl, ok := result.Template.(map[string]any)
	require.True(t, ok, "expected template to decode as an object, got %T", result.Template)

	resources, ok := tmpl["resources"].([]any)
	require.True(t, ok, "expected resources to decode as an array, got %T", tmpl["resources"])
	require.Len(t, resources, 1, "exportTemplate must enumerate the VM in the group")

	entry, ok := resources[0].(map[string]any)
	require.True(t, ok, "expected a resource entry object, got %T", resources[0])

	assert.Equal(t, "microsoft.compute/virtualmachines", entry["type"])
	assert.Equal(t, inst[0].ID, entry["name"])
	assert.Equal(t, "eastus", entry["location"])
	assert.NotEmpty(t, entry["apiVersion"])

	// A group export scoped to a name that isn't the VM's own group stays
	// empty — exportTemplate must not leak resources across groups.
	_, err = rgClient.CreateOrUpdate(ctx, "otherrg", armresources.ResourceGroup{
		Location: to.Ptr("eastus"),
	}, nil)
	require.NoError(t, err)

	otherPoller, err := rgClient.BeginExportTemplate(ctx, "otherrg", armresources.ExportTemplateRequest{
		Resources: []*string{to.Ptr("*")},
	}, nil)
	require.NoError(t, err)

	otherResult, err := otherPoller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	otherTmpl, ok := otherResult.Template.(map[string]any)
	require.True(t, ok)

	otherResources, ok := otherTmpl["resources"].([]any)
	require.True(t, ok)
	assert.Empty(t, otherResources, "exportTemplate for an unrelated group must not include the VM")
}
