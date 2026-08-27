package virtualmachines_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestSDKVMSSDelete is the regression test for the VMSS DELETE fix: BeginDelete
// on a scale set must return 202 + a terminal async operation the SDK poller
// settles, and a follow-up Get must report NotFound. Before the fix serveScaleSet
// answered DELETE with 501 NotImplemented.
func TestSDKVMSSDelete(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{VirtualMachines: cloudP.VirtualMachines})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client, err := armcompute.NewVirtualMachineScaleSetsClient("sub-1", fakeCred{}, sdkClientOptions(ts))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	createVMSS(ctx, t, client, "rg-1", "del-vmss")

	deletePoller, err := client.BeginDelete(ctx, "rg-1", "del-vmss", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := deletePoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("Delete poll: %v", err)
	}

	if _, err := client.Get(ctx, "rg-1", "del-vmss", nil); !isNotFound(err) {
		t.Fatalf("Get after delete: err=%v, want 404 NotFound", err)
	}
}

// TestSDKVMSSResourceGroupCascade asserts a scale set does not survive its
// resource group: deleting the RG cascades into the VM handler's
// PurgeResourceGroup, which now tears down scale sets too.
func TestSDKVMSSResourceGroupCascade(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{VirtualMachines: cloudP.VirtualMachines})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client, err := armcompute.NewVirtualMachineScaleSetsClient("sub-1", fakeCred{}, sdkClientOptions(ts))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// The RG must exist for its DELETE to cascade (a delete of an absent group is
	// a no-op 204 with no cascade), so create it, then the scale set inside it.
	putResourceGroup(ctx, t, ts, "sub-1", "rg-cascade")
	createVMSS(ctx, t, client, "rg-cascade", "cascade-vmss")

	deleteResourceGroup(ctx, t, ts, "sub-1", "rg-cascade")

	if _, err := client.Get(ctx, "rg-cascade", "cascade-vmss", nil); !isNotFound(err) {
		t.Fatalf("Get scale set after RG delete: err=%v, want 404 NotFound (VMSS must not survive its RG)", err)
	}
}

func createVMSS(ctx context.Context, t *testing.T, client *armcompute.VirtualMachineScaleSetsClient, rg, name string) {
	t.Helper()

	poller, err := client.BeginCreateOrUpdate(ctx, rg, name, armcompute.VirtualMachineScaleSet{
		Location: to.Ptr("eastus"),
		SKU: &armcompute.SKU{
			Name:     to.Ptr("Standard_D2s_v3"),
			Tier:     to.Ptr("Standard"),
			Capacity: to.Ptr[int64](2),
		},
	}, nil)
	if err != nil {
		t.Fatalf("VMSS BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("VMSS create poll: %v", err)
	}
}

// putResourceGroup / deleteResourceGroup drive the ARM resource-group endpoints
// directly (there is no compute SDK for them) so the cascade can be exercised.
func putResourceGroup(ctx context.Context, t *testing.T, ts *httptest.Server, sub, name string) {
	t.Helper()

	url := ts.URL + "/subscriptions/" + sub + "/resourcegroups/" + name + "?api-version=2021-04-01"
	body := strings.NewReader(`{"location":"eastus"}`)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, body)
	if err != nil {
		t.Fatalf("new RG PUT request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("RG PUT: %v", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("RG PUT status=%d, want 200/201", resp.StatusCode)
	}
}

func deleteResourceGroup(ctx context.Context, t *testing.T, ts *httptest.Server, sub, name string) {
	t.Helper()

	url := ts.URL + "/subscriptions/" + sub + "/resourcegroups/" + name + "?api-version=2021-04-01"

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		t.Fatalf("new RG DELETE request: %v", err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("RG DELETE: %v", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("RG DELETE status=%d, want 202/204", resp.StatusCode)
	}
}

func isNotFound(err error) bool {
	var respErr *azcore.ResponseError

	return errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound
}
