// Real-SDK regression for A1: Azure Resource Graph rows for non-compute
// resources must report the resource's real resourceGroup and Azure region
// (not the literal "default" / an AWS-style "us-east-1"), and must never leak
// the internal "cloudemu:"-prefixed ARM bookkeeping tags. Resources are created
// through the live ARM wire (armnetwork / armcompute) so the resource-group tag
// and region metadata the walker reads are actually populated, then discovered
// through the live armresourcegraph client.

package resourcegraph_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

func armClientOpts(t *testing.T, ts *httptest.Server) *arm.ClientOptions {
	t.Helper()

	return &arm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Cloud: cloud.Configuration{Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		}},
		Transport: ts.Client(),
		Retry:     policy.RetryOptions{MaxRetries: -1},
	}}
}

// rowByType returns the first ARG row whose `type` equals typ, failing the test
// when none is present.
func rowByType(t *testing.T, data []any, typ string) map[string]any {
	t.Helper()

	for _, d := range data {
		row, ok := d.(map[string]any)
		if ok && row["type"] == typ {
			return row
		}
	}

	t.Fatalf("no ARG row of type %q in %d rows", typ, len(data))

	return nil
}

// TestSDKResourceGraph_RealResourceGroupAndLocation pins A1: an Azure VNet
// created under a specific resource group and region reports both faithfully in
// its Resource Graph row, and a VM row carries no internal cloudemu:* tag.
func TestSDKResourceGraph_RealResourceGroupAndLocation(t *testing.T) {
	ctx := context.Background()
	cloudP := cloudemu.NewAzure()

	const (
		rg     = "rg-prod"
		region = "westus2"
	)

	srv := azureserver.New(azureserver.Drivers{
		Network:           cloudP.VNet,
		VirtualMachines:   cloudP.VirtualMachines,
		ResourceDiscovery: cloudP.ResourceDiscovery,
		SubscriptionID:    "123456789012",
	})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	opts := armClientOpts(t, ts)

	// Create a VNet through the ARM wire so the resource-group tag and region
	// metadata the discovery walker reads are actually populated.
	vnets, err := armnetwork.NewVirtualNetworksClient("123456789012", fakeCred{}, opts)
	require.NoError(t, err)

	vnetPoller, err := vnets.BeginCreateOrUpdate(ctx, rg, "vnet-app", armnetwork.VirtualNetwork{
		Location: to.Ptr(region),
		Tags:     map[string]*string{"env": to.Ptr("prod")},
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr("10.0.0.0/16")}},
		},
	}, nil)
	require.NoError(t, err)

	_, err = vnetPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	require.NoError(t, err)

	// Create a VM through the ARM wire so it carries the internal cloudemu:azureName
	// tag the wire handler stamps — the tag that must not leak into an ARG row.
	vms, err := armcompute.NewVirtualMachinesClient("123456789012", fakeCred{}, opts)
	require.NoError(t, err)

	vmPoller, err := vms.BeginCreateOrUpdate(ctx, rg, "vm-app", armcompute.VirtualMachine{
		Location: to.Ptr(region),
		Tags:     map[string]*string{"env": to.Ptr("prod")},
		Properties: &armcompute.VirtualMachineProperties{
			HardwareProfile: &armcompute.HardwareProfile{VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardD2SV3)},
		},
	}, nil)
	require.NoError(t, err)

	_, err = vmPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	require.NoError(t, err)

	client := newResourceGraphClient(t, ts)

	out, err := client.Resources(ctx, armresourcegraph.QueryRequest{Query: to.Ptr("Resources")}, nil)
	require.NoError(t, err)

	data, ok := out.Data.([]any)
	require.True(t, ok, "expected []any data, got %T", out.Data)

	t.Run("vnet reports its real resourceGroup and Azure region", func(t *testing.T) {
		row := rowByType(t, data, "microsoft.network/virtualnetworks")

		assert.Equal(t, rg, row["resourceGroup"],
			"VNet row must carry its real resource group, not the literal \"default\"")
		assert.Equal(t, region, row["location"],
			"VNet row must carry its real Azure region, not the AWS-style engine default")
		assert.Contains(t, row["id"].(string), "/resourceGroups/"+rg+"/",
			"the VNet resource id must embed its real resource group")

		// Real user tags survive; internal ones do not.
		tags := row["tags"].(map[string]any)
		assert.Equal(t, "prod", tags["env"])
		assertNoInternalTags(t, tags)
	})

	t.Run("vm row carries no internal cloudemu:* tag", func(t *testing.T) {
		row := rowByType(t, data, "microsoft.compute/virtualmachines")

		tags := row["tags"].(map[string]any)
		assert.Equal(t, "prod", tags["env"], "the real user tag survives")
		assertNoInternalTags(t, tags)
	})
}

func assertNoInternalTags(t *testing.T, tags map[string]any) {
	t.Helper()

	for k := range tags {
		assert.Falsef(t, strings.HasPrefix(k, "cloudemu:"),
			"internal tag %q leaked into the ARG row", k)
	}
}
