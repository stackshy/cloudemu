package virtualmachines_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
)

// TestSDKDataDiskUnmodeledFieldRoundTrips is the load-bearing test for the echo
// overlay's array recursion on VMs: a data disk attached with a sub-field the
// handler does not model (writeAcceleratorEnabled) must reflect that sub-field
// on the VM's storageProfile.dataDisks entry at GET — before the fix it was
// dropped because the overlay only recursed into maps, never array elements —
// while the modeled fields the handler rebuilds from real attachment state
// (lun, diskSizeGB, managedDisk.id) stay authoritative and no phantom disk is
// injected.
func TestSDKDataDiskUnmodeledFieldRoundTrips(t *testing.T) {
	vmClient, diskClient := newDataDiskTestServer(t)
	ctx := context.Background()

	vmPoller, err := vmClient.BeginCreateOrUpdate(ctx, "rg-1", "vm-wa",
		armcompute.VirtualMachine{
			Location: to.Ptr("eastus"),
			Properties: &armcompute.VirtualMachineProperties{
				HardwareProfile: &armcompute.HardwareProfile{
					VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardD2SV3),
				},
				OSProfile: &armcompute.OSProfile{ComputerName: to.Ptr("vm-wa"), AdminUsername: to.Ptr("azureuser")},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate VM: %v", err)
	}

	if _, err := pollUntilDone(ctx, vmPoller); err != nil {
		t.Fatalf("CreateOrUpdate VM poll: %v", err)
	}

	disk, err := createDataDisk(ctx, t, diskClient, "disk-wa")
	if err != nil {
		t.Fatalf("create disk-wa: %v", err)
	}

	// Attach the disk at lun 0, carrying an unmodeled sub-field
	// (writeAcceleratorEnabled) alongside the modeled managedDisk reference.
	attachPoller, err := vmClient.BeginCreateOrUpdate(ctx, "rg-1", "vm-wa",
		armcompute.VirtualMachine{
			Location: to.Ptr("eastus"),
			Properties: &armcompute.VirtualMachineProperties{
				HardwareProfile: &armcompute.HardwareProfile{
					VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardD2SV3),
				},
				StorageProfile: &armcompute.StorageProfile{
					DataDisks: []*armcompute.DataDisk{{
						Lun:                     to.Ptr[int32](0),
						CreateOption:            to.Ptr(armcompute.DiskCreateOptionTypesAttach),
						ManagedDisk:             &armcompute.ManagedDiskParameters{ID: disk.ID},
						WriteAcceleratorEnabled: to.Ptr(true),
					}},
				},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate attach: %v", err)
	}

	attached, err := pollUntilDone(ctx, attachPoller)
	if err != nil {
		t.Fatalf("CreateOrUpdate attach poll: %v", err)
	}

	// The unmodeled sub-field must survive on the create response too.
	assertDataDiskWriteAccel(t, attached.Properties.StorageProfile, disk.ID)

	// And, the real point of the fix, on a later GET.
	got, err := vmClient.Get(ctx, "rg-1", "vm-wa", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	assertDataDiskWriteAccel(t, got.Properties.StorageProfile, disk.ID)
}

// assertDataDiskWriteAccel asserts the VM has exactly one data disk at lun 0
// whose modeled fields (diskSizeGB, managedDisk.id) are authoritative AND whose
// unmodeled writeAcceleratorEnabled round-tripped true.
func assertDataDiskWriteAccel(t *testing.T, sp *armcompute.StorageProfile, wantDiskID *string) {
	t.Helper()

	if sp == nil {
		t.Fatalf("storageProfile is nil, want one data disk")
	}

	if len(sp.DataDisks) != 1 {
		t.Fatalf("dataDisks=%d entries, want exactly 1 (no phantom elements)", len(sp.DataDisks))
	}

	d := sp.DataDisks[0]

	if d.Lun == nil || *d.Lun != 0 {
		t.Errorf("dataDisk lun=%v, want 0", d.Lun)
	}

	if d.DiskSizeGB == nil || *d.DiskSizeGB != 32 {
		t.Errorf("dataDisk diskSizeGB=%v, want 32 (modeled)", d.DiskSizeGB)
	}

	if d.ManagedDisk == nil || d.ManagedDisk.ID == nil || wantDiskID == nil || *d.ManagedDisk.ID != *wantDiskID {
		t.Errorf("dataDisk managedDisk.id=%v, want %v (modeled)", managedDiskID(d), derefStr(wantDiskID))
	}

	if d.WriteAcceleratorEnabled == nil || !*d.WriteAcceleratorEnabled {
		t.Errorf("dataDisk dropped unmodeled writeAcceleratorEnabled: got %v", d.WriteAcceleratorEnabled)
	}
}

func managedDiskID(d *armcompute.DataDisk) string {
	if d == nil || d.ManagedDisk == nil {
		return "<nil>"
	}

	return derefStr(d.ManagedDisk.ID)
}
