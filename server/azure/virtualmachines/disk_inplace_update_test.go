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

// TestSDKDiskInPlaceUpdateOnAttachedDisk is the regression test for the disk
// CreateOrUpdate fix: re-PUTting a disk that is already attached to a VM must
// update it IN PLACE — exactly one backing volume, the new tag applied, and the
// derived uniqueId + timeCreated unchanged from the first PUT. The old code did
// delete+create, which (a) silently failed the delete on an attached disk and
// created a duplicate phantom volume, and (b) regenerated the volume id, churning
// uniqueId + timeCreated on every re-PUT.
func TestSDKDiskInPlaceUpdateOnAttachedDisk(t *testing.T) {
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

	ctx := context.Background()

	// A VM to attach the disk to.
	vmPoller, err := vmClient.BeginCreateOrUpdate(ctx, "rg-1", "vm-inplace",
		armcompute.VirtualMachine{
			Location: to.Ptr("eastus"),
			Properties: &armcompute.VirtualMachineProperties{
				HardwareProfile: &armcompute.HardwareProfile{
					VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardD2SV3),
				},
				OSProfile: &armcompute.OSProfile{ComputerName: to.Ptr("vm-inplace"), AdminUsername: to.Ptr("azureuser")},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate VM: %v", err)
	}

	if _, err := pollUntilDone(ctx, vmPoller); err != nil {
		t.Fatalf("CreateOrUpdate VM poll: %v", err)
	}

	created, err := createDataDisk(ctx, t, diskClient, "d-att")
	if err != nil {
		t.Fatalf("create d-att: %v", err)
	}

	firstUID := diskUID(t, created)
	firstCreated := diskTime(t, created)

	// Attach d-att to the VM via PATCH (merge-patch attach at lun 0).
	attachPoller, err := vmClient.BeginUpdate(ctx, "rg-1", "vm-inplace",
		armcompute.VirtualMachineUpdate{
			Properties: &armcompute.VirtualMachineProperties{
				StorageProfile: &armcompute.StorageProfile{
					DataDisks: []*armcompute.DataDisk{{
						Lun:          to.Ptr[int32](0),
						CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesAttach),
						ManagedDisk:  &armcompute.ManagedDiskParameters{ID: created.ID},
					}},
				},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate attach: %v", err)
	}

	if _, err := attachPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("attach poll: %v", err)
	}

	// Re-PUT the (now attached) disk adding a tag — the operation that used to
	// duplicate the volume and drop the update.
	rePoller, err := diskClient.BeginCreateOrUpdate(ctx, "rg-1", "d-att", armcompute.Disk{
		Location: to.Ptr("eastus"),
		SKU:      &armcompute.DiskSKU{Name: to.Ptr(armcompute.DiskStorageAccountTypesPremiumLRS)},
		Tags:     map[string]*string{"stage": to.Ptr("qa")},
		Properties: &armcompute.DiskProperties{
			CreationData: &armcompute.CreationData{CreateOption: to.Ptr(armcompute.DiskCreateOptionEmpty)},
			DiskSizeGB:   to.Ptr[int32](32),
		},
	}, nil)
	if err != nil {
		t.Fatalf("re-PUT d-att: %v", err)
	}

	if _, err := rePoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("re-PUT poll: %v", err)
	}

	// Exactly ONE backing volume — no duplicate phantom.
	vols, err := cloudP.VirtualMachines.DescribeVolumes(ctx, nil)
	if err != nil {
		t.Fatalf("DescribeVolumes: %v", err)
	}

	if len(vols) != 1 {
		t.Fatalf("backing volumes=%d, want exactly 1 (re-PUT of an attached disk must not duplicate it)", len(vols))
	}

	got, err := diskClient.Get(ctx, "rg-1", "d-att", nil)
	if err != nil {
		t.Fatalf("Get d-att: %v", err)
	}

	// Tag applied.
	if got.Tags == nil || tagVal(got.Tags, "stage") != "qa" {
		t.Errorf("tag stage=%q, want qa (re-PUT update was dropped)", tagVal(got.Tags, "stage"))
	}

	// Still attached (in-place update preserved the attachment).
	if got.Properties == nil || got.Properties.DiskState == nil || *got.Properties.DiskState != armcompute.DiskStateAttached {
		t.Errorf("diskState=%v, want Attached", diskStateOf(got))
	}

	// uniqueId + timeCreated stable across the update.
	if uid := diskUID(t, got.Disk); uid != firstUID {
		t.Errorf("uniqueId=%q, want %q (must be stable across an in-place update)", uid, firstUID)
	}

	if tc := diskTime(t, got.Disk); tc != firstCreated {
		t.Errorf("timeCreated=%q, want %q (must be stable across an in-place update)", tc, firstCreated)
	}
}

func diskUID(t *testing.T, d armcompute.Disk) string {
	t.Helper()

	if d.Properties == nil || d.Properties.UniqueID == nil {
		t.Fatalf("disk uniqueId is nil")
	}

	return *d.Properties.UniqueID
}

func diskTime(t *testing.T, d armcompute.Disk) string {
	t.Helper()

	if d.Properties == nil || d.Properties.TimeCreated == nil {
		t.Fatalf("disk timeCreated is nil")
	}

	return d.Properties.TimeCreated.Format(time.RFC3339Nano)
}
