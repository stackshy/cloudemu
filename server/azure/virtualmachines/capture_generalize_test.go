package virtualmachines_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// createSDKVM provisions a VM through the real SDK and polls to completion.
func createSDKVM(t *testing.T, client *armcompute.VirtualMachinesClient, name string) {
	t.Helper()

	poller, err := client.BeginCreateOrUpdate(context.Background(), "rg-1", name,
		armcompute.VirtualMachine{
			Location: to.Ptr("eastus"),
			Properties: &armcompute.VirtualMachineProperties{
				HardwareProfile: &armcompute.HardwareProfile{
					VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardD2SV3),
				},
				StorageProfile: &armcompute.StorageProfile{
					OSDisk: &armcompute.OSDisk{OSType: to.Ptr(armcompute.OperatingSystemTypesLinux)},
				},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate %s: %v", name, err)
	}

	if _, err := poller.PollUntilDone(context.Background(),
		&runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("create poll %s: %v", name, err)
	}
}

// TestSDKGeneralizeAndCapture verifies the golden-image workflow: capturing a
// VM before it is generalized is rejected, and after Generalize the Capture
// action returns a VirtualMachineCaptureResult template.
func TestSDKGeneralizeAndCapture(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{VirtualMachines: cloudP.VirtualMachines})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newSDKClient(t, ts)
	ctx := context.Background()

	createSDKVM(t, client, "img-vm")

	// Capture before generalize must be rejected.
	if _, err := client.BeginCapture(ctx, "rg-1", "img-vm",
		armcompute.VirtualMachineCaptureParameters{
			VhdPrefix:                to.Ptr("cap"),
			DestinationContainerName: to.Ptr("vhds"),
			OverwriteVhds:            to.Ptr(true),
		}, nil); err == nil {
		t.Fatal("expected Capture of a non-generalized VM to fail")
	}

	if _, err := client.Generalize(ctx, "rg-1", "img-vm", nil); err != nil {
		t.Fatalf("Generalize: %v", err)
	}

	poller, err := client.BeginCapture(ctx, "rg-1", "img-vm",
		armcompute.VirtualMachineCaptureParameters{
			VhdPrefix:                to.Ptr("cap"),
			DestinationContainerName: to.Ptr("vhds"),
			OverwriteVhds:            to.Ptr(true),
		}, nil)
	if err != nil {
		t.Fatalf("BeginCapture: %v", err)
	}

	res, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	if err != nil {
		t.Fatalf("capture poll: %v", err)
	}

	if res.ID == nil || *res.ID == "" {
		t.Error("capture result missing id")
	}

	if len(res.Resources) == 0 {
		t.Error("capture result missing resources template")
	}
}

// TestSDKGeneralizeMissingVM asserts Generalize on a VM that does not exist
// returns an error.
func TestSDKGeneralizeMissingVM(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{VirtualMachines: cloudP.VirtualMachines})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newSDKClient(t, ts)

	if _, err := client.Generalize(context.Background(), "rg-1", "ghost", nil); err == nil {
		t.Fatal("expected Generalize of a missing VM to fail")
	}
}

// TestSDKCreateVMMissingNICFails verifies that when a networking driver is
// wired, creating a VM whose networkProfile references a non-existent NIC is
// rejected rather than silently succeeding.
func TestSDKCreateVMMissingNICFails(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		VirtualMachines: cloudP.VirtualMachines,
		Network:         cloudP.VNet,
	})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newSDKClient(t, ts)
	ctx := context.Background()

	nicID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/networkInterfaces/nope"

	_, err := client.BeginCreateOrUpdate(ctx, "rg-1", "bad-nic-vm",
		armcompute.VirtualMachine{
			Location: to.Ptr("eastus"),
			Properties: &armcompute.VirtualMachineProperties{
				HardwareProfile: &armcompute.HardwareProfile{
					VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardD2SV3),
				},
				NetworkProfile: &armcompute.NetworkProfile{
					NetworkInterfaces: []*armcompute.NetworkInterfaceReference{
						{ID: to.Ptr(nicID)},
					},
				},
			},
		}, nil)
	if err == nil {
		t.Fatal("expected VM create with a missing NIC to fail")
	}
}
