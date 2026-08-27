// Deep-audit regression (N1): deleting a resource group did not cascade to the
// resources created under it. A teardown "succeeded" (the group vanished) while
// every storage account, virtual network, etc. inside it stayed alive and
// globally addressable — leaking resources and colliding on the next apply. A
// resource group is a pure container, so its delete must remove the resources
// it holds.

package resourcegroups_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

func armOpts(ts *httptest.Server) *arm.ClientOptions {
	return &arm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Cloud: cloud.Configuration{Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		}},
		Transport: ts.Client(),
		Retry:     policy.RetryOptions{MaxRetries: -1},
	}}
}

func statusCode(t *testing.T, err error) int {
	t.Helper()

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		t.Fatalf("expected an azcore.ResponseError, got %v", err)
	}

	return respErr.StatusCode
}

// TestResourceGroupDeleteCascades creates a resource group with a storage
// account and a virtual network inside it (through the real ARM wire API), then
// deletes the group and asserts both contained resources are gone (404), not
// merely the group.
func TestResourceGroupDeleteCascades(t *testing.T) {
	ctx := context.Background()
	cloudP := cloudemu.NewAzure()

	ts := httptest.NewTLSServer(azureserver.NewFromProvider(cloudP))
	t.Cleanup(ts.Close)

	opts := armOpts(ts)

	rgClient, err := armresources.NewResourceGroupsClient(subID, fakeCred{}, opts)
	require.NoError(t, err)

	storageClient, err := armstorage.NewAccountsClient(subID, fakeCred{}, opts)
	require.NoError(t, err)

	vnetClient, err := armnetwork.NewVirtualNetworksClient(subID, fakeCred{}, opts)
	require.NoError(t, err)

	const rg = "rg-cascade"

	_, err = rgClient.CreateOrUpdate(ctx, rg, armresources.ResourceGroup{Location: to.Ptr("eastus")}, nil)
	require.NoError(t, err)

	// Storage account inside the group.
	saPoller, err := storageClient.BeginCreate(ctx, rg, "sacascade", armstorage.AccountCreateParameters{
		Kind:     to.Ptr(armstorage.KindStorageV2),
		Location: to.Ptr("eastus"),
		SKU:      &armstorage.SKU{Name: to.Ptr(armstorage.SKUNameStandardLRS)},
	}, nil)
	require.NoError(t, err)
	_, err = saPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	require.NoError(t, err)

	// Virtual network inside the group.
	vnetPoller, err := vnetClient.BeginCreateOrUpdate(ctx, rg, "vnetcascade", armnetwork.VirtualNetwork{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr("10.0.0.0/16")}},
		},
	}, nil)
	require.NoError(t, err)
	_, err = vnetPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	require.NoError(t, err)

	// Both resources are reachable before the group delete.
	_, err = storageClient.GetProperties(ctx, rg, "sacascade", nil)
	require.NoError(t, err, "storage account should exist before RG delete")

	_, err = vnetClient.Get(ctx, rg, "vnetcascade", nil)
	require.NoError(t, err, "vnet should exist before RG delete")

	// Delete the resource group.
	delPoller, err := rgClient.BeginDelete(ctx, rg, nil)
	require.NoError(t, err)
	_, err = delPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	require.NoError(t, err)

	// The contained resources must be gone, not just the group.
	if _, gerr := storageClient.GetProperties(ctx, rg, "sacascade", nil); gerr == nil {
		t.Fatal("storage account survived resource-group delete (no cascade)")
	} else if code := statusCode(t, gerr); code != 404 {
		t.Fatalf("storage account after RG delete: status %d, want 404", code)
	}

	if _, gerr := vnetClient.Get(ctx, rg, "vnetcascade", nil); gerr == nil {
		t.Fatal("vnet survived resource-group delete (no cascade)")
	} else if code := statusCode(t, gerr); code != 404 {
		t.Fatalf("vnet after RG delete: status %d, want 404", code)
	}
}
