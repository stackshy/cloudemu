package virtualmachines_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
)

// TestSDKVMOSDiskMaterialized verifies that creating a VM whose storageProfile
// declares an osDisk materializes a real Microsoft.Compute/disks resource: a
// real azure-sdk-for-go DisksClient can Get the OS disk, and it reports the
// requested size, createOption, SKU, Attached state, and a managedBy pointing
// back at the VM — the disks API returned nothing for a VM's OS disk before.
func TestSDKVMOSDiskMaterialized(t *testing.T) {
	vmClient, diskClient := newDataDiskTestServer(t)
	ctx := context.Background()

	vm := createVMWithOSDisk(ctx, t, vmClient, "vm-os", "vm-os_osdisk",
		armcompute.DiskDeleteOptionTypesDelete, nil)

	osDisk, err := diskClient.Get(ctx, "rg-1", "vm-os_osdisk", nil)
	if err != nil {
		t.Fatalf("Get OS disk: %v", err)
	}

	if osDisk.Properties == nil || osDisk.Properties.DiskSizeGB == nil || *osDisk.Properties.DiskSizeGB != 64 {
		t.Errorf("OS disk diskSizeGB=%v, want 64", osDiskSize(osDisk))
	}

	if osDisk.Properties.DiskState == nil || *osDisk.Properties.DiskState != armcompute.DiskStateAttached {
		t.Errorf("OS disk diskState=%v, want Attached", diskStateOf(osDisk))
	}

	if osDisk.ManagedBy == nil || *osDisk.ManagedBy != *vm.ID {
		t.Errorf("OS disk managedBy=%v, want %v", derefStr(osDisk.ManagedBy), *vm.ID)
	}

	if osDisk.Properties.CreationData == nil || osDisk.Properties.CreationData.CreateOption == nil ||
		*osDisk.Properties.CreationData.CreateOption != armcompute.DiskCreateOptionFromImage {
		t.Errorf("OS disk createOption=%v, want FromImage", createOptionOf(osDisk))
	}

	if osDisk.SKU == nil || osDisk.SKU.Name == nil || *osDisk.SKU.Name != armcompute.DiskStorageAccountTypesPremiumLRS {
		t.Errorf("OS disk sku=%v, want Premium_LRS", osDiskSKU(osDisk))
	}

	// The OS disk is not reported as a data disk (it attaches at a non-LUN
	// device), so a GET on the VM shows no data disks.
	got, err := vmClient.Get(ctx, "rg-1", "vm-os", nil)
	if err != nil {
		t.Fatalf("Get VM: %v", err)
	}

	assertDataDiskLUNs(t, got.Properties.StorageProfile)
}

// TestSDKVMImplicitEmptyDataDisk verifies that a VM created with a
// storageProfile.dataDisks entry using createOption=Empty materializes a real
// managed disk (not just a silent skip): the disks API returns the new disk at
// its LUN, sized and attached, and the VM's dataDisks reflects the LUN.
func TestSDKVMImplicitEmptyDataDisk(t *testing.T) {
	vmClient, diskClient := newDataDiskTestServer(t)
	ctx := context.Background()

	poller, err := vmClient.BeginCreateOrUpdate(ctx, "rg-1", "vm-empty",
		armcompute.VirtualMachine{
			Location: to.Ptr("eastus"),
			Properties: &armcompute.VirtualMachineProperties{
				HardwareProfile: &armcompute.HardwareProfile{
					VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardD2SV3),
				},
				StorageProfile: &armcompute.StorageProfile{
					DataDisks: []*armcompute.DataDisk{{
						Lun:          to.Ptr[int32](2),
						Name:         to.Ptr("vm-empty_data2"),
						CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesEmpty),
						DiskSizeGB:   to.Ptr[int32](128),
						ManagedDisk: &armcompute.ManagedDiskParameters{
							StorageAccountType: to.Ptr(armcompute.StorageAccountTypesStandardSSDLRS),
						},
					}},
				},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate VM: %v", err)
	}

	vm, err := pollUntilDone(ctx, poller)
	if err != nil {
		t.Fatalf("CreateOrUpdate poll: %v", err)
	}

	// The implicit Empty disk must now exist as a real disk resource.
	dataDisk, err := diskClient.Get(ctx, "rg-1", "vm-empty_data2", nil)
	if err != nil {
		t.Fatalf("Get implicit data disk: %v", err)
	}

	if dataDisk.Properties == nil || dataDisk.Properties.DiskSizeGB == nil || *dataDisk.Properties.DiskSizeGB != 128 {
		t.Errorf("data disk diskSizeGB=%v, want 128", osDiskSize(dataDisk))
	}

	if dataDisk.Properties.DiskState == nil || *dataDisk.Properties.DiskState != armcompute.DiskStateAttached {
		t.Errorf("data disk diskState=%v, want Attached", diskStateOf(dataDisk))
	}

	if dataDisk.ManagedBy == nil || *dataDisk.ManagedBy != *vm.ID {
		t.Errorf("data disk managedBy=%v, want %v", derefStr(dataDisk.ManagedBy), *vm.ID)
	}

	// And it is attached to the VM at lun 2.
	got, err := vmClient.Get(ctx, "rg-1", "vm-empty", nil)
	if err != nil {
		t.Fatalf("Get VM: %v", err)
	}

	assertDataDiskLUNs(t, got.Properties.StorageProfile, 2)
}

// TestSDKVMDeleteDeleteOptionCascade verifies the deleteOption cascade on VM
// delete: an OS disk with deleteOption=Delete is deleted with the VM (Get 404s),
// while an implicit data disk with deleteOption=Detach survives, returned to the
// Unattached state with its managedBy cleared.
func TestSDKVMDeleteDeleteOptionCascade(t *testing.T) {
	vmClient, diskClient := newDataDiskTestServer(t)
	ctx := context.Background()

	dataDisks := []*armcompute.DataDisk{{
		Lun:          to.Ptr[int32](0),
		Name:         to.Ptr("vm-cascade_keep0"),
		CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesEmpty),
		DiskSizeGB:   to.Ptr[int32](16),
		DeleteOption: to.Ptr(armcompute.DiskDeleteOptionTypesDetach),
	}}

	createVMWithOSDisk(ctx, t, vmClient, "vm-cascade", "vm-cascade_osdisk",
		armcompute.DiskDeleteOptionTypesDelete, dataDisks)

	// Both disks exist and are attached before delete.
	if _, err := diskClient.Get(ctx, "rg-1", "vm-cascade_osdisk", nil); err != nil {
		t.Fatalf("OS disk should exist before delete: %v", err)
	}

	if _, err := diskClient.Get(ctx, "rg-1", "vm-cascade_keep0", nil); err != nil {
		t.Fatalf("data disk should exist before delete: %v", err)
	}

	delPoller, err := vmClient.BeginDelete(ctx, "rg-1", "vm-cascade", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Delete poll: %v", err)
	}

	// The OS disk (deleteOption=Delete) is gone.
	if _, err := diskClient.Get(ctx, "rg-1", "vm-cascade_osdisk", nil); !isNotFound(err) {
		t.Errorf("OS disk after delete: err=%v, want 404 NotFound", err)
	}

	// The data disk (deleteOption=Detach) survives, now Unattached.
	survivor, err := diskClient.Get(ctx, "rg-1", "vm-cascade_keep0", nil)
	if err != nil {
		t.Fatalf("data disk should survive Detach: %v", err)
	}

	if survivor.Properties == nil || survivor.Properties.DiskState == nil ||
		*survivor.Properties.DiskState != armcompute.DiskStateUnattached {
		t.Errorf("survivor diskState=%v, want Unattached", diskStateOf(survivor))
	}

	if survivor.ManagedBy != nil && *survivor.ManagedBy != "" {
		t.Errorf("survivor managedBy=%v after VM delete, want empty", *survivor.ManagedBy)
	}
}

// createVMWithOSDisk creates a VM with a Premium_LRS FromImage OS disk named
// osDiskName carrying deleteOption, plus any data disks, and returns the VM.
func createVMWithOSDisk(
	ctx context.Context, t *testing.T, vmClient *armcompute.VirtualMachinesClient,
	vmName, osDiskName string, osDeleteOption armcompute.DiskDeleteOptionTypes, dataDisks []*armcompute.DataDisk,
) armcompute.VirtualMachine {
	t.Helper()

	poller, err := vmClient.BeginCreateOrUpdate(ctx, "rg-1", vmName,
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
					OSDisk: &armcompute.OSDisk{
						Name:         to.Ptr(osDiskName),
						OSType:       to.Ptr(armcompute.OperatingSystemTypesLinux),
						CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesFromImage),
						DiskSizeGB:   to.Ptr[int32](64),
						DeleteOption: to.Ptr(osDeleteOption),
						ManagedDisk: &armcompute.ManagedDiskParameters{
							StorageAccountType: to.Ptr(armcompute.StorageAccountTypesPremiumLRS),
						},
					},
					DataDisks: dataDisks,
				},
				OSProfile: &armcompute.OSProfile{ComputerName: to.Ptr(vmName), AdminUsername: to.Ptr("azureuser")},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate %s: %v", vmName, err)
	}

	vm, err := pollUntilDone(ctx, poller)
	if err != nil {
		t.Fatalf("CreateOrUpdate %s poll: %v", vmName, err)
	}

	return vm
}

func osDiskSize(d armcompute.DisksClientGetResponse) string {
	if d.Properties == nil || d.Properties.DiskSizeGB == nil {
		return "<nil>"
	}

	return strconv.Itoa(int(*d.Properties.DiskSizeGB))
}

func createOptionOf(d armcompute.DisksClientGetResponse) string {
	if d.Properties == nil || d.Properties.CreationData == nil || d.Properties.CreationData.CreateOption == nil {
		return "<nil>"
	}

	return string(*d.Properties.CreationData.CreateOption)
}

func osDiskSKU(d armcompute.DisksClientGetResponse) string {
	if d.SKU == nil || d.SKU.Name == nil {
		return "<nil>"
	}

	return string(*d.SKU.Name)
}
