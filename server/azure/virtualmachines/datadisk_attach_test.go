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

// newDataDiskTestServer wires a TLS server backed by a single Azure compute
// mock, exposing both virtualMachines and disks — a real deployment where
// the same VM handles attaches managed disks created through the disks API.
func newDataDiskTestServer(t *testing.T) (*armcompute.VirtualMachinesClient, *armcompute.DisksClient) {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		VirtualMachines: cloudP.VirtualMachines,
		Disks:           cloudP.VirtualMachines,
	})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	vmClient := newSDKClient(t, ts)

	diskClient, err := armcompute.NewDisksClient("sub-1", fakeCred{}, sdkClientOptions(ts))
	if err != nil {
		t.Fatal(err)
	}

	return vmClient, diskClient
}

// TestSDKVMDataDiskAttachDetach is the load-bearing regression test for the
// VM managed-disk attach fix: a real azure-sdk-for-go client creates a VM and
// two managed disks, attaches them through both the declarative PUT
// CreateOrUpdate path and the merge-patch PATCH Update path, and verifies the
// attachment is real — reflected on GET (storageProfile.dataDisks) and on the
// disk resource itself (managedBy) — then detaches one via PATCH toBeDetached.
func TestSDKVMDataDiskAttachDetach(t *testing.T) {
	vmClient, diskClient := newDataDiskTestServer(t)
	ctx := context.Background()

	vmPoller, err := vmClient.BeginCreateOrUpdate(ctx, "rg-1", "vm-disks",
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
				OSProfile: &armcompute.OSProfile{ComputerName: to.Ptr("vm-disks"), AdminUsername: to.Ptr("azureuser")},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate VM: %v", err)
	}

	if _, err := pollUntilDone(ctx, vmPoller); err != nil {
		t.Fatalf("CreateOrUpdate VM poll: %v", err)
	}

	disk1, err := createDataDisk(ctx, t, diskClient, "disk-0")
	if err != nil {
		t.Fatalf("create disk-0: %v", err)
	}

	disk2, err := createDataDisk(ctx, t, diskClient, "disk-1")
	if err != nil {
		t.Fatalf("create disk-1: %v", err)
	}

	// PUT the VM again with dataDisks referencing disk-0: the standard
	// declarative attach path. The blocker was that this was a no-op.
	attachPoller, err := vmClient.BeginCreateOrUpdate(ctx, "rg-1", "vm-disks",
		armcompute.VirtualMachine{
			Location: to.Ptr("eastus"),
			Properties: &armcompute.VirtualMachineProperties{
				HardwareProfile: &armcompute.HardwareProfile{
					VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardD2SV3),
				},
				StorageProfile: &armcompute.StorageProfile{
					DataDisks: []*armcompute.DataDisk{{
						Lun:          to.Ptr[int32](0),
						CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesAttach),
						ManagedDisk:  &armcompute.ManagedDiskParameters{ID: disk1.ID},
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

	assertDataDiskLUNs(t, attached.Properties.StorageProfile, 0)

	// GET must reflect the real attachment (not just echo the request).
	got, err := vmClient.Get(ctx, "rg-1", "vm-disks", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	assertDataDiskLUNs(t, got.Properties.StorageProfile, 0)

	// The disk's own managedBy must now point at the VM.
	gotDisk1, err := diskClient.Get(ctx, "rg-1", "disk-0", nil)
	if err != nil {
		t.Fatalf("Get disk-0: %v", err)
	}

	if gotDisk1.ManagedBy == nil || *gotDisk1.ManagedBy != *got.ID {
		t.Errorf("disk-0 managedBy=%v, want %v", derefStr(gotDisk1.ManagedBy), *got.ID)
	}

	if gotDisk1.Properties == nil || gotDisk1.Properties.DiskState == nil || *gotDisk1.Properties.DiskState != armcompute.DiskStateAttached {
		t.Errorf("disk-0 diskState=%v, want Attached", diskStateOf(gotDisk1))
	}

	// PATCH (BeginUpdate) adds disk-1 at lun 1 via the real read-modify-write
	// pattern: the desired dataDisks list carries BOTH lun 0 (kept) and lun 1
	// (new). A PATCH's supplied dataDisks array is a full replace, so keeping an
	// existing attachment means re-listing it — the same way `az vm disk attach`
	// GETs, appends, then updates.
	updatePoller, err := vmClient.BeginUpdate(ctx, "rg-1", "vm-disks",
		armcompute.VirtualMachineUpdate{
			Properties: &armcompute.VirtualMachineProperties{
				StorageProfile: &armcompute.StorageProfile{
					DataDisks: []*armcompute.DataDisk{{
						Lun:          to.Ptr[int32](0),
						CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesAttach),
						ManagedDisk:  &armcompute.ManagedDiskParameters{ID: disk1.ID},
					}, {
						Lun:          to.Ptr[int32](1),
						CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesAttach),
						ManagedDisk:  &armcompute.ManagedDiskParameters{ID: disk2.ID},
					}},
				},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate attach lun1: %v", err)
	}

	if _, err := updatePoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("Update attach lun1 poll: %v", err)
	}

	got, err = vmClient.Get(ctx, "rg-1", "vm-disks", nil)
	if err != nil {
		t.Fatalf("Get after PATCH attach: %v", err)
	}

	assertDataDiskLUNs(t, got.Properties.StorageProfile, 0, 1)

	// PATCH detaches lun 0 via toBeDetached while keeping lun 1 — real Azure's
	// graceful-detach shape (list all disks, mark the one to detach). lun 1 is
	// re-listed so the full-replace PATCH keeps it attached.
	detachPoller, err := vmClient.BeginUpdate(ctx, "rg-1", "vm-disks",
		armcompute.VirtualMachineUpdate{
			Properties: &armcompute.VirtualMachineProperties{
				StorageProfile: &armcompute.StorageProfile{
					DataDisks: []*armcompute.DataDisk{{
						Lun:          to.Ptr[int32](0),
						ToBeDetached: to.Ptr(true),
					}, {
						Lun:          to.Ptr[int32](1),
						CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesAttach),
						ManagedDisk:  &armcompute.ManagedDiskParameters{ID: disk2.ID},
					}},
				},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate detach lun0: %v", err)
	}

	if _, err := detachPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("Update detach lun0 poll: %v", err)
	}

	got, err = vmClient.Get(ctx, "rg-1", "vm-disks", nil)
	if err != nil {
		t.Fatalf("Get after PATCH detach: %v", err)
	}

	// lun 0 detached; lun 1 (attached via PATCH, untouched by this PATCH)
	// must survive — proving Update is merge-patch, not declarative.
	assertDataDiskLUNs(t, got.Properties.StorageProfile, 1)

	gotDisk1, err = diskClient.Get(ctx, "rg-1", "disk-0", nil)
	if err != nil {
		t.Fatalf("Get disk-0 after detach: %v", err)
	}

	if gotDisk1.ManagedBy != nil && *gotDisk1.ManagedBy != "" {
		t.Errorf("disk-0 managedBy=%v after detach, want empty", *gotDisk1.ManagedBy)
	}
}

// TestSDKVMDataDiskAttach_ReplaceAtSameLUN is the regression test for the
// LUN-replacement fix: PUTting a VM whose dataDisks references a different
// managed disk at a LUN that already has one attached must detach the old
// disk (clearing its managedBy) before attaching the new one, rather than
// leaving both disks mapped to the same LUN.
func TestSDKVMDataDiskAttach_ReplaceAtSameLUN(t *testing.T) {
	vmClient, diskClient := newDataDiskTestServer(t)
	ctx := context.Background()

	vmPoller, err := vmClient.BeginCreateOrUpdate(ctx, "rg-1", "vm-relun",
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
				OSProfile: &armcompute.OSProfile{ComputerName: to.Ptr("vm-relun"), AdminUsername: to.Ptr("azureuser")},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate VM: %v", err)
	}

	if _, err := pollUntilDone(ctx, vmPoller); err != nil {
		t.Fatalf("CreateOrUpdate VM poll: %v", err)
	}

	diskA, err := createDataDisk(ctx, t, diskClient, "disk-a")
	if err != nil {
		t.Fatalf("create disk-a: %v", err)
	}

	diskB, err := createDataDisk(ctx, t, diskClient, "disk-b")
	if err != nil {
		t.Fatalf("create disk-b: %v", err)
	}

	// Attach disk A at lun 0.
	attachPoller, err := vmClient.BeginCreateOrUpdate(ctx, "rg-1", "vm-relun",
		armcompute.VirtualMachine{
			Location: to.Ptr("eastus"),
			Properties: &armcompute.VirtualMachineProperties{
				HardwareProfile: &armcompute.HardwareProfile{
					VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardD2SV3),
				},
				StorageProfile: &armcompute.StorageProfile{
					DataDisks: []*armcompute.DataDisk{{
						Lun:          to.Ptr[int32](0),
						CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesAttach),
						ManagedDisk:  &armcompute.ManagedDiskParameters{ID: diskA.ID},
					}},
				},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate attach disk-a: %v", err)
	}

	if _, err := pollUntilDone(ctx, attachPoller); err != nil {
		t.Fatalf("CreateOrUpdate attach disk-a poll: %v", err)
	}

	// Re-declaring lun 0 with disk B must detach disk A first — not leave
	// both disks mapped to lun 0.
	replacePoller, err := vmClient.BeginCreateOrUpdate(ctx, "rg-1", "vm-relun",
		armcompute.VirtualMachine{
			Location: to.Ptr("eastus"),
			Properties: &armcompute.VirtualMachineProperties{
				HardwareProfile: &armcompute.HardwareProfile{
					VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardD2SV3),
				},
				StorageProfile: &armcompute.StorageProfile{
					DataDisks: []*armcompute.DataDisk{{
						Lun:          to.Ptr[int32](0),
						CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesAttach),
						ManagedDisk:  &armcompute.ManagedDiskParameters{ID: diskB.ID},
					}},
				},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate replace lun0 with disk-b: %v", err)
	}

	got, err := pollUntilDone(ctx, replacePoller)
	if err != nil {
		t.Fatalf("CreateOrUpdate replace lun0 poll: %v", err)
	}

	// Exactly one disk at lun 0.
	assertDataDiskLUNs(t, got.Properties.StorageProfile, 0)

	if len(got.Properties.StorageProfile.DataDisks) != 1 {
		t.Fatalf("dataDisks=%d entries, want exactly 1", len(got.Properties.StorageProfile.DataDisks))
	}

	gotLunDisk := got.Properties.StorageProfile.DataDisks[0]
	if gotLunDisk.ManagedDisk == nil || gotLunDisk.ManagedDisk.ID == nil || *gotLunDisk.ManagedDisk.ID != *diskB.ID {
		t.Errorf("lun 0 managedDisk.id=%v, want disk-b %v", gotLunDisk.ManagedDisk, *diskB.ID)
	}

	// Disk A must be detached: managedBy cleared, diskState back to Unattached.
	gotDiskA, err := diskClient.Get(ctx, "rg-1", "disk-a", nil)
	if err != nil {
		t.Fatalf("Get disk-a: %v", err)
	}

	if gotDiskA.ManagedBy != nil && *gotDiskA.ManagedBy != "" {
		t.Errorf("disk-a managedBy=%v after replacement, want empty", *gotDiskA.ManagedBy)
	}

	if gotDiskA.Properties == nil || gotDiskA.Properties.DiskState == nil || *gotDiskA.Properties.DiskState != armcompute.DiskStateUnattached {
		t.Errorf("disk-a diskState=%v, want Unattached", diskStateOf(gotDiskA))
	}

	// Disk B must be attached at lun 0.
	gotDiskB, err := diskClient.Get(ctx, "rg-1", "disk-b", nil)
	if err != nil {
		t.Fatalf("Get disk-b: %v", err)
	}

	if gotDiskB.ManagedBy == nil || *gotDiskB.ManagedBy != *got.ID {
		t.Errorf("disk-b managedBy=%v, want %v", derefStr(gotDiskB.ManagedBy), *got.ID)
	}

	if gotDiskB.Properties == nil || gotDiskB.Properties.DiskState == nil || *gotDiskB.Properties.DiskState != armcompute.DiskStateAttached {
		t.Errorf("disk-b diskState=%v, want Attached", diskStateOf(gotDiskB))
	}
}

func createDataDisk(ctx context.Context, t *testing.T, diskClient *armcompute.DisksClient, name string) (armcompute.Disk, error) {
	t.Helper()

	poller, err := diskClient.BeginCreateOrUpdate(ctx, "rg-1", name, armcompute.Disk{
		Location: to.Ptr("eastus"),
		SKU:      &armcompute.DiskSKU{Name: to.Ptr(armcompute.DiskStorageAccountTypesPremiumLRS)},
		Properties: &armcompute.DiskProperties{
			CreationData: &armcompute.CreationData{CreateOption: to.Ptr(armcompute.DiskCreateOptionEmpty)},
			DiskSizeGB:   to.Ptr[int32](32),
		},
	}, nil)
	if err != nil {
		return armcompute.Disk{}, err
	}

	resp, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	if err != nil {
		return armcompute.Disk{}, err
	}

	return resp.Disk, nil
}

func assertDataDiskLUNs(t *testing.T, sp *armcompute.StorageProfile, wantLUNs ...int32) {
	t.Helper()

	if sp == nil {
		if len(wantLUNs) > 0 {
			t.Fatalf("storageProfile is nil, want dataDisks at luns %v", wantLUNs)
		}

		return
	}

	got := make(map[int32]bool, len(sp.DataDisks))

	for _, d := range sp.DataDisks {
		if d.Lun != nil {
			got[*d.Lun] = true
		}
	}

	if len(got) != len(wantLUNs) {
		t.Fatalf("dataDisks luns=%v, want %v", got, wantLUNs)
	}

	for _, lun := range wantLUNs {
		if !got[lun] {
			t.Errorf("dataDisks missing lun %d, got %v", lun, got)
		}
	}
}

func diskStateOf(d armcompute.DisksClientGetResponse) string {
	if d.Properties == nil || d.Properties.DiskState == nil {
		return "<nil>"
	}

	return string(*d.Properties.DiskState)
}

func derefStr(s *string) string {
	if s == nil {
		return "<nil>"
	}

	return *s
}
