package virtualmachines_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// createSDKNIC provisions a network interface with a single static
// ipConfiguration through the real armnetwork SDK and polls to completion,
// returning its ARM resource id.
func createSDKNIC(t *testing.T, nicClient *armnetwork.InterfacesClient, name, privateIP string) string {
	t.Helper()

	poller, err := nicClient.BeginCreateOrUpdate(context.Background(), "rg-1", name, armnetwork.Interface{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.InterfacePropertiesFormat{
			IPConfigurations: []*armnetwork.InterfaceIPConfiguration{{
				Name: to.Ptr("ipconfig1"),
				Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
					PrivateIPAddress:          to.Ptr(privateIP),
					PrivateIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodStatic),
				},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("nic BeginCreateOrUpdate %s: %v", name, err)
	}

	if _, err := poller.PollUntilDone(context.Background(),
		&runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("nic poll %s: %v", name, err)
	}

	return "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/networkInterfaces/" + name
}

// createSDKVMWithNIC provisions a VM referencing nicID in its networkProfile
// through the real armcompute SDK and polls to completion, returning the
// created VM's ARM resource id.
func createSDKVMWithNIC(
	t *testing.T, client *armcompute.VirtualMachinesClient, name, nicID string,
) (string, error) {
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
				NetworkProfile: &armcompute.NetworkProfile{
					NetworkInterfaces: []*armcompute.NetworkInterfaceReference{{ID: to.Ptr(nicID)}},
				},
			},
		}, nil)
	if err != nil {
		return "", err
	}

	created, err := poller.PollUntilDone(context.Background(),
		&runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	if err != nil {
		return "", err
	}

	if created.ID == nil {
		t.Fatalf("create %s: response has no id", name)
	}

	return *created.ID, nil
}

// TestSDKCreateVMSetsNICBackReference is the load-bearing regression test for
// the NIC<->VM cross-reference: creating a VM whose networkProfile references
// a NIC must set that NIC's properties.virtualMachine.id, a second VM cannot
// attach an already-attached NIC, and deleting the owning VM clears the
// back-reference again (real Azure's InUseNetworkInterfaceCannotBeDeleted /
// attach semantics — see MS Learn "Add or remove network interfaces": a NIC
// belongs to exactly one VM at a time).
func TestSDKCreateVMSetsNICBackReference(t *testing.T) {
	// AccountID matches the "sub-1" subscription the SDK clients below address,
	// mirroring a correctly-configured deployment (cloudemu serve
	// --azure-subscription); the driver stamps VM resource ids from this
	// account id, and they must equal the subscription callers see in the URL.
	cloudP := cloudemu.NewAzure(config.WithAccountID("sub-1"))
	srv := azureserver.New(azureserver.Drivers{
		VirtualMachines: cloudP.VirtualMachines,
		Network:         cloudP.VNet,
	})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()

	nicClient, err := armnetwork.NewInterfacesClient("sub-1", fakeCred{}, sdkClientOptions(ts))
	if err != nil {
		t.Fatal(err)
	}

	nicID := createSDKNIC(t, nicClient, "nic-vm-1", "10.0.1.50")

	vmClient := newSDKClient(t, ts)

	vmID, err := createSDKVMWithNIC(t, vmClient, "vm-1", nicID)
	if err != nil {
		t.Fatalf("create vm-1: %v", err)
	}

	// GET NIC must now report the owning VM.
	got, err := nicClient.Get(ctx, "rg-1", "nic-vm-1", nil)
	if err != nil {
		t.Fatalf("nic Get: %v", err)
	}

	if got.Properties == nil || got.Properties.VirtualMachine == nil || got.Properties.VirtualMachine.ID == nil {
		t.Fatal("nic properties.virtualMachine not set after VM create")
	}

	if *got.Properties.VirtualMachine.ID != vmID {
		t.Errorf("nic virtualMachine.id=%q want %q", *got.Properties.VirtualMachine.ID, vmID)
	}

	// A second VM cannot attach the same, already-attached NIC.
	if _, err := createSDKVMWithNIC(t, vmClient, "vm-2", nicID); err == nil {
		t.Fatal("expected creating a second VM with an already-attached NIC to fail")
	}

	// Deleting the owning VM must clear the NIC's back-reference.
	delPoller, err := vmClient.BeginDelete(ctx, "rg-1", "vm-1", nil)
	if err != nil {
		t.Fatalf("BeginDelete vm-1: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("delete poll vm-1: %v", err)
	}

	afterDelete, err := nicClient.Get(ctx, "rg-1", "nic-vm-1", nil)
	if err != nil {
		t.Fatalf("nic Get after delete: %v", err)
	}

	if afterDelete.Properties != nil && afterDelete.Properties.VirtualMachine != nil {
		t.Errorf("nic virtualMachine ref=%v, want cleared after owning VM delete", afterDelete.Properties.VirtualMachine)
	}

	// The NIC is now unattached and can be attached to a fresh VM.
	if _, err := createSDKVMWithNIC(t, vmClient, "vm-3", nicID); err != nil {
		t.Errorf("re-attach freed nic to vm-3: %v", err)
	}
}
