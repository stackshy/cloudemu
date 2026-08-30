package images_test

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

// TestSDKImageFromDisk exercises creating a managed image directly from a source
// OS disk (no sourceVirtualMachine) and verifies Get round-trips osType/osState/
// diskSizeGB and the source managedDisk reference.
func TestSDKImageFromDisk(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		VirtualMachines: cloudP.VirtualMachines,
		Disks:           cloudP.VirtualMachines,
		Images:          cloudP.VirtualMachines,
	})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()
	opts := clientOpts(ts)

	// Create the source managed disk.
	diskClient, err := armcompute.NewDisksClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	diskPoller, err := diskClient.BeginCreateOrUpdate(ctx, "rg-1", "os-disk",
		armcompute.Disk{
			Location: to.Ptr("eastus"),
			SKU:      &armcompute.DiskSKU{Name: to.Ptr(armcompute.DiskStorageAccountTypesPremiumLRS)},
			Properties: &armcompute.DiskProperties{
				CreationData: &armcompute.CreationData{CreateOption: to.Ptr(armcompute.DiskCreateOptionEmpty)},
				DiskSizeGB:   to.Ptr[int32](64),
			},
		}, nil)
	if err != nil {
		t.Fatalf("disk create: %v", err)
	}

	if _, err := diskPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("disk poll: %v", err)
	}

	diskID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/disks/os-disk"

	// Create the image from that disk — no sourceVirtualMachine.
	imgClient, err := armcompute.NewImagesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	imgPoller, err := imgClient.BeginCreateOrUpdate(ctx, "rg-1", "img-from-disk",
		armcompute.Image{
			Location: to.Ptr("eastus"),
			Properties: &armcompute.ImageProperties{
				StorageProfile: &armcompute.ImageStorageProfile{
					OSDisk: &armcompute.ImageOSDisk{
						OSType:      to.Ptr(armcompute.OperatingSystemTypesLinux),
						OSState:     to.Ptr(armcompute.OperatingSystemStateTypesGeneralized),
						ManagedDisk: &armcompute.SubResource{ID: to.Ptr(diskID)},
					},
				},
			},
		}, nil)
	if err != nil {
		t.Fatalf("img BeginCreateOrUpdate: %v", err)
	}

	if _, err := imgPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("img poll: %v", err)
	}

	got, err := imgClient.Get(ctx, "rg-1", "img-from-disk", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	osDisk := got.Properties.StorageProfile.OSDisk
	if osDisk == nil {
		t.Fatal("storageProfile.osDisk missing on disk-sourced image")
	}

	if osDisk.OSType == nil || *osDisk.OSType != armcompute.OperatingSystemTypesLinux {
		t.Errorf("osType=%v want Linux", osDisk.OSType)
	}

	if osDisk.OSState == nil || *osDisk.OSState != armcompute.OperatingSystemStateTypesGeneralized {
		t.Errorf("osState=%v want Generalized", osDisk.OSState)
	}

	if osDisk.DiskSizeGB == nil || *osDisk.DiskSizeGB != 64 {
		t.Errorf("diskSizeGB=%v want 64", osDisk.DiskSizeGB)
	}

	if osDisk.ManagedDisk == nil || osDisk.ManagedDisk.ID == nil || *osDisk.ManagedDisk.ID != diskID {
		t.Errorf("managedDisk.id=%v want %s", osDisk.ManagedDisk, diskID)
	}

	// A disk-sourced image carries no VM capture reference.
	if got.Properties.SourceVirtualMachine != nil {
		t.Errorf("sourceVirtualMachine should be absent, got %v", got.Properties.SourceVirtualMachine)
	}
}

// TestSDKImageFromDiskMissingDisk verifies creating an image from a disk that
// does not exist fails rather than silently producing an empty image.
func TestSDKImageFromDiskMissingDisk(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		VirtualMachines: cloudP.VirtualMachines,
		Disks:           cloudP.VirtualMachines,
		Images:          cloudP.VirtualMachines,
	})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()
	opts := clientOpts(ts)

	imgClient, err := armcompute.NewImagesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	missing := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/disks/ghost"

	poller, err := imgClient.BeginCreateOrUpdate(ctx, "rg-1", "img-ghost",
		armcompute.Image{
			Location: to.Ptr("eastus"),
			Properties: &armcompute.ImageProperties{
				StorageProfile: &armcompute.ImageStorageProfile{
					OSDisk: &armcompute.ImageOSDisk{
						OSType:      to.Ptr(armcompute.OperatingSystemTypesLinux),
						OSState:     to.Ptr(armcompute.OperatingSystemStateTypesGeneralized),
						ManagedDisk: &armcompute.SubResource{ID: to.Ptr(missing)},
					},
				},
			},
		}, nil)
	if err == nil {
		_, err = poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	}

	if err == nil {
		t.Fatal("expected error creating image from a non-existent disk")
	}
}
