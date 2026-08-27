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

// putVMWithNICs drives a PUT CreateOrUpdate against an existing VM whose
// networkProfile references nicIDs (the first entry marked primary), through
// the real armcompute SDK, and polls to completion. It backs the in-place
// update path (repeat PUT to the same {rg,name}) that must reconcile NIC
// attach/detach.
func putVMWithNICs(
	t *testing.T, client *armcompute.VirtualMachinesClient, name string, nicIDs ...string,
) error {
	t.Helper()

	refs := make([]*armcompute.NetworkInterfaceReference, 0, len(nicIDs))

	for i, id := range nicIDs {
		refs = append(refs, &armcompute.NetworkInterfaceReference{
			ID: to.Ptr(id),
			Properties: &armcompute.NetworkInterfaceReferenceProperties{
				Primary: to.Ptr(i == 0),
			},
		})
	}

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
				NetworkProfile: &armcompute.NetworkProfile{NetworkInterfaces: refs},
			},
		}, nil)
	if err != nil {
		return err
	}

	_, err = poller.PollUntilDone(context.Background(), &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})

	return err
}

// nicVMRef returns the ARM id of the VM a NIC is currently attached to, or ""
// when the NIC has no properties.virtualMachine back-reference.
func nicVMRef(t *testing.T, nicClient *armnetwork.InterfacesClient, name string) string {
	t.Helper()

	got, err := nicClient.Get(context.Background(), "rg-1", name, nil)
	if err != nil {
		t.Fatalf("nic Get %s: %v", name, err)
	}

	if got.Properties == nil || got.Properties.VirtualMachine == nil || got.Properties.VirtualMachine.ID == nil {
		return ""
	}

	return *got.Properties.VirtualMachine.ID
}

// newReconcileFixture stands up an Azure wire server with VM + networking
// drivers wired and returns the VM and NIC SDK clients pointed at it.
func newReconcileFixture(t *testing.T) (*armcompute.VirtualMachinesClient, *armnetwork.InterfacesClient) {
	t.Helper()

	cloudP := cloudemu.NewAzure(config.WithAccountID("sub-1"))
	srv := azureserver.New(azureserver.Drivers{
		VirtualMachines: cloudP.VirtualMachines,
		Network:         cloudP.VNet,
	})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	nicClient, err := armnetwork.NewInterfacesClient("sub-1", fakeCred{}, sdkClientOptions(ts))
	if err != nil {
		t.Fatal(err)
	}

	return newSDKClient(t, ts), nicClient
}

// TestSDKVMUpdateReconcilesNICSwap covers the in-place-update NIC reconcile fix:
// a VM created with NIC-A, then PUT again pointing at NIC-B, must move the
// attachment — NIC-B gains the virtualMachine back-reference, NIC-A loses it —
// and, having been detached, NIC-A can now be deleted (no longer InUse /
// InUseNetworkInterfaceCannotBeDeleted). Before the fix UpdateInstance ignored
// networkProfile.networkInterfaces, so NIC-B never attached and NIC-A stayed
// permanently in use.
func TestSDKVMUpdateReconcilesNICSwap(t *testing.T) {
	vmClient, nicClient := newReconcileFixture(t)
	ctx := context.Background()

	nicA := createSDKNIC(t, nicClient, "nic-a", "10.0.1.10")
	nicB := createSDKNIC(t, nicClient, "nic-b", "10.0.1.11")

	vmID, err := createSDKVMWithNIC(t, vmClient, "vm-swap", nicA)
	if err != nil {
		t.Fatalf("create vm-swap: %v", err)
	}

	if ref := nicVMRef(t, nicClient, "nic-a"); ref != vmID {
		t.Fatalf("nic-a back-ref=%q want %q after create", ref, vmID)
	}

	// (a) PUT the same VM pointing at NIC-B: the association moves to NIC-B.
	if err := putVMWithNICs(t, vmClient, "vm-swap", nicB); err != nil {
		t.Fatalf("PUT vm-swap with nic-b: %v", err)
	}

	if ref := nicVMRef(t, nicClient, "nic-b"); ref != vmID {
		t.Errorf("nic-b back-ref=%q want %q after update", ref, vmID)
	}

	if ref := nicVMRef(t, nicClient, "nic-a"); ref != "" {
		t.Errorf("nic-a back-ref=%q want cleared after update", ref)
	}

	// (b) NIC-A, now detached, is no longer InUse and can be deleted.
	delPoller, err := nicClient.BeginDelete(ctx, "rg-1", "nic-a", nil)
	if err != nil {
		t.Fatalf("BeginDelete nic-a: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("delete nic-a (should be detached, not InUse): %v", err)
	}
}

// TestSDKVMPatchWithoutNetworkProfilePreservesNICs covers requirement (c): a
// PATCH Update that carries no networkProfile must not drop the VM's NICs. The
// PATCH routes through PatchInstance (merge-patch) and must leave the existing
// attachment intact, matching ARM's RFC 7386 semantics.
func TestSDKVMPatchWithoutNetworkProfilePreservesNICs(t *testing.T) {
	vmClient, nicClient := newReconcileFixture(t)
	ctx := context.Background()

	nicA := createSDKNIC(t, nicClient, "nic-keep", "10.0.1.20")

	vmID, err := createSDKVMWithNIC(t, vmClient, "vm-keep", nicA)
	if err != nil {
		t.Fatalf("create vm-keep: %v", err)
	}

	// PATCH resizing the VM (no network profile at all).
	patchPoller, err := vmClient.BeginUpdate(ctx, "rg-1", "vm-keep",
		armcompute.VirtualMachineUpdate{
			Properties: &armcompute.VirtualMachineProperties{
				HardwareProfile: &armcompute.HardwareProfile{
					VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardD4SV3),
				},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate vm-keep: %v", err)
	}

	if _, err := patchPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("patch poll vm-keep: %v", err)
	}

	if ref := nicVMRef(t, nicClient, "nic-keep"); ref != vmID {
		t.Errorf("nic-keep back-ref=%q want %q preserved across PATCH (no wipe)", ref, vmID)
	}
}

// TestSDKVMUpdateAddsSecondNIC covers requirement (d): a PUT that adds a second
// NIC to a VM already holding one attaches both, each with the virtualMachine
// back-reference, keeping the first-listed NIC primary. The previously attached
// NIC is left attached (attach is idempotent on the same VM), not detached.
func TestSDKVMUpdateAddsSecondNIC(t *testing.T) {
	vmClient, nicClient := newReconcileFixture(t)

	nic1 := createSDKNIC(t, nicClient, "nic-1", "10.0.1.30")
	nic2 := createSDKNIC(t, nicClient, "nic-2", "10.0.1.31")

	vmID, err := createSDKVMWithNIC(t, vmClient, "vm-multi", nic1)
	if err != nil {
		t.Fatalf("create vm-multi: %v", err)
	}

	// PUT with both NICs (nic-1 primary, nic-2 added).
	if err := putVMWithNICs(t, vmClient, "vm-multi", nic1, nic2); err != nil {
		t.Fatalf("PUT vm-multi with two NICs: %v", err)
	}

	if ref := nicVMRef(t, nicClient, "nic-1"); ref != vmID {
		t.Errorf("nic-1 back-ref=%q want %q (primary, still attached)", ref, vmID)
	}

	if ref := nicVMRef(t, nicClient, "nic-2"); ref != vmID {
		t.Errorf("nic-2 back-ref=%q want %q (newly attached)", ref, vmID)
	}
}

// TestSDKVMUpdateMissingNICRejected covers requirement (e): a PUT whose
// networkProfile references a NIC that does not exist is rejected with
// NetworkInterfaceNotFound, exactly as the create path validates, and leaves the
// VM's current attachment untouched.
func TestSDKVMUpdateMissingNICRejected(t *testing.T) {
	vmClient, nicClient := newReconcileFixture(t)

	nicA := createSDKNIC(t, nicClient, "nic-present", "10.0.1.40")

	vmID, err := createSDKVMWithNIC(t, vmClient, "vm-missing", nicA)
	if err != nil {
		t.Fatalf("create vm-missing: %v", err)
	}

	missing := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/networkInterfaces/nic-ghost"

	if err := putVMWithNICs(t, vmClient, "vm-missing", missing); err == nil {
		t.Fatal("expected PUT referencing a missing NIC to fail with NetworkInterfaceNotFound")
	}

	// The rejected update must not have disturbed the existing attachment.
	if ref := nicVMRef(t, nicClient, "nic-present"); ref != vmID {
		t.Errorf("nic-present back-ref=%q want %q unchanged after rejected update", ref, vmID)
	}
}
