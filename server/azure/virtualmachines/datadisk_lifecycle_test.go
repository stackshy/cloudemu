package virtualmachines_test

import (
	"context"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
)

// TestSDKVMPATCHDataDiskDetachByOmission is the regression test for the PATCH
// (BeginUpdate) declarative-detach fix: `az vm update` / BeginUpdate remove a
// data disk by sending the modified (shorter) storageProfile.dataDisks array.
// A PATCH that supplies a dataDisks array must detach every disk whose LUN is
// absent from it (a supplied empty array detaches all), clearing each disk's
// managedBy/diskState — matching real Azure — while a PATCH that omits the
// array entirely stays a merge-patch and leaves attachments untouched.
func TestSDKVMPATCHDataDiskDetachByOmission(t *testing.T) {
	vmClient, diskClient := newDataDiskTestServer(t)
	ctx := context.Background()

	createBareVM(ctx, t, vmClient, "vm-det")

	diskA, err := createDataDisk(ctx, t, diskClient, "det-a")
	if err != nil {
		t.Fatalf("create det-a: %v", err)
	}

	diskB, err := createDataDisk(ctx, t, diskClient, "det-b")
	if err != nil {
		t.Fatalf("create det-b: %v", err)
	}

	// Attach both disks via PUT (lun 0 = det-a, lun 1 = det-b).
	putDataDisks(ctx, t, vmClient, "vm-det", []*armcompute.DataDisk{
		{Lun: to.Ptr[int32](0), CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesAttach),
			ManagedDisk: &armcompute.ManagedDiskParameters{ID: diskA.ID}},
		{Lun: to.Ptr[int32](1), CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesAttach),
			ManagedDisk: &armcompute.ManagedDiskParameters{ID: diskB.ID}},
	})

	vm, err := vmClient.Get(ctx, "rg-1", "vm-det", nil)
	if err != nil {
		t.Fatalf("Get after attach: %v", err)
	}

	assertDataDiskLUNs(t, vm.Properties.StorageProfile, 0, 1)

	// PATCH with a dataDisks list that supplies ONLY lun 1 (omitting lun 0):
	// real Azure's full-replace-on-PATCH detaches lun 0's disk. This was the
	// bug — the PATCH path treated the shorter array as a no-op.
	patchDataDisks(ctx, t, vmClient, "vm-det", []*armcompute.DataDisk{
		{Lun: to.Ptr[int32](1), CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesAttach),
			ManagedDisk: &armcompute.ManagedDiskParameters{ID: diskB.ID}},
	})

	vm, err = vmClient.Get(ctx, "rg-1", "vm-det", nil)
	if err != nil {
		t.Fatalf("Get after PATCH omit lun0: %v", err)
	}

	// lun 0 detached; lun 1 survives.
	assertDataDiskLUNs(t, vm.Properties.StorageProfile, 1)

	assertDiskDetached(ctx, t, diskClient, "det-a")

	// PATCH with an explicit EMPTY dataDisks array detaches everything that
	// remains (lun 1). An empty non-nil array is a full replace with no disks.
	patchDataDisks(ctx, t, vmClient, "vm-det", []*armcompute.DataDisk{})

	vm, err = vmClient.Get(ctx, "rg-1", "vm-det", nil)
	if err != nil {
		t.Fatalf("Get after PATCH empty: %v", err)
	}

	assertDataDiskLUNs(t, vm.Properties.StorageProfile)

	assertDiskDetached(ctx, t, diskClient, "det-b")
}

// TestSDKVMDeleteClearsDiskManagedBy is the regression test for the VM-delete
// disk-lifecycle fix: deleting a VM with an attached data disk (default
// deleteOption=Detach — the disk survives) must clear the disk's managedBy and
// return it to Unattached, rather than leaving it dangling at the now-deleted
// VM. A cleared disk is re-attachable to another VM.
func TestSDKVMDeleteClearsDiskManagedBy(t *testing.T) {
	vmClient, diskClient := newDataDiskTestServer(t)
	ctx := context.Background()

	createBareVM(ctx, t, vmClient, "vm-del")

	disk, err := createDataDisk(ctx, t, diskClient, "del-disk")
	if err != nil {
		t.Fatalf("create del-disk: %v", err)
	}

	putDataDisks(ctx, t, vmClient, "vm-del", []*armcompute.DataDisk{
		{Lun: to.Ptr[int32](0), CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesAttach),
			ManagedDisk: &armcompute.ManagedDiskParameters{ID: disk.ID}},
	})

	// Sanity: the disk is attached before delete.
	got, err := diskClient.Get(ctx, "rg-1", "del-disk", nil)
	if err != nil {
		t.Fatalf("Get del-disk before delete: %v", err)
	}

	if got.ManagedBy == nil || *got.ManagedBy == "" {
		t.Fatalf("del-disk managedBy empty before delete, want set")
	}

	// Delete the VM.
	delPoller, err := vmClient.BeginDelete(ctx, "rg-1", "vm-del", nil)
	if err != nil {
		t.Fatalf("BeginDelete vm-del: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("Delete vm-del poll: %v", err)
	}

	// The disk survives with managedBy cleared and diskState Unattached.
	assertDiskDetached(ctx, t, diskClient, "del-disk")

	// A cleared disk is re-attachable: attach it to a fresh VM.
	createBareVM(ctx, t, vmClient, "vm-del2")

	putDataDisks(ctx, t, vmClient, "vm-del2", []*armcompute.DataDisk{
		{Lun: to.Ptr[int32](0), CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesAttach),
			ManagedDisk: &armcompute.ManagedDiskParameters{ID: disk.ID}},
	})

	vm2, err := vmClient.Get(ctx, "rg-1", "vm-del2", nil)
	if err != nil {
		t.Fatalf("Get vm-del2: %v", err)
	}

	assertDataDiskLUNs(t, vm2.Properties.StorageProfile, 0)

	reattached, err := diskClient.Get(ctx, "rg-1", "del-disk", nil)
	if err != nil {
		t.Fatalf("Get del-disk after reattach: %v", err)
	}

	if reattached.ManagedBy == nil || *reattached.ManagedBy != *vm2.ID {
		t.Errorf("del-disk managedBy=%v after reattach, want %v", derefStr(reattached.ManagedBy), *vm2.ID)
	}
}

// createBareVM provisions a data-disk-less VM through the SDK create path.
func createBareVM(ctx context.Context, t *testing.T, vmClient *armcompute.VirtualMachinesClient, name string) {
	t.Helper()

	poller, err := vmClient.BeginCreateOrUpdate(ctx, "rg-1", name,
		armcompute.VirtualMachine{
			Location: to.Ptr("eastus"),
			Properties: &armcompute.VirtualMachineProperties{
				HardwareProfile: &armcompute.HardwareProfile{
					VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardD2SV3),
				},
				StorageProfile: &armcompute.StorageProfile{
					ImageReference: &armcompute.ImageReference{
						Publisher: to.Ptr("Canonical"), Offer: to.Ptr("UbuntuServer"),
						SKU: to.Ptr("22.04-LTS"), Version: to.Ptr("latest"),
					},
				},
				OSProfile: &armcompute.OSProfile{ComputerName: to.Ptr(name), AdminUsername: to.Ptr("azureuser")},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate %s: %v", name, err)
	}

	if _, err := pollUntilDone(ctx, poller); err != nil {
		t.Fatalf("CreateOrUpdate %s poll: %v", name, err)
	}
}

// putDataDisks re-PUTs a VM with the given dataDisks set (declarative attach).
func putDataDisks(
	ctx context.Context, t *testing.T, vmClient *armcompute.VirtualMachinesClient, name string, disks []*armcompute.DataDisk,
) {
	t.Helper()

	poller, err := vmClient.BeginCreateOrUpdate(ctx, "rg-1", name,
		armcompute.VirtualMachine{
			Location: to.Ptr("eastus"),
			Properties: &armcompute.VirtualMachineProperties{
				HardwareProfile: &armcompute.HardwareProfile{
					VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardD2SV3),
				},
				StorageProfile: &armcompute.StorageProfile{DataDisks: disks},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate %s dataDisks: %v", name, err)
	}

	if _, err := pollUntilDone(ctx, poller); err != nil {
		t.Fatalf("CreateOrUpdate %s dataDisks poll: %v", name, err)
	}
}

// patchDataDisks PATCHes (BeginUpdate) a VM with the given dataDisks array.
// A non-nil (empty included) array is a full replace of the disk set.
func patchDataDisks(
	ctx context.Context, t *testing.T, vmClient *armcompute.VirtualMachinesClient, name string, disks []*armcompute.DataDisk,
) {
	t.Helper()

	poller, err := vmClient.BeginUpdate(ctx, "rg-1", name,
		armcompute.VirtualMachineUpdate{
			Properties: &armcompute.VirtualMachineProperties{
				StorageProfile: &armcompute.StorageProfile{DataDisks: disks},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate %s dataDisks: %v", name, err)
	}

	if _, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("Update %s dataDisks poll: %v", name, err)
	}
}

// assertDiskDetached verifies a managed disk is detached: managedBy cleared and
// diskState back to Unattached.
func assertDiskDetached(ctx context.Context, t *testing.T, diskClient *armcompute.DisksClient, name string) {
	t.Helper()

	got, err := diskClient.Get(ctx, "rg-1", name, nil)
	if err != nil {
		t.Fatalf("Get %s: %v", name, err)
	}

	if got.ManagedBy != nil && *got.ManagedBy != "" {
		t.Errorf("%s managedBy=%v, want empty", name, *got.ManagedBy)
	}

	if got.Properties == nil || got.Properties.DiskState == nil || *got.Properties.DiskState != armcompute.DiskStateUnattached {
		t.Errorf("%s diskState=%v, want Unattached", name, diskStateOf(got))
	}
}
