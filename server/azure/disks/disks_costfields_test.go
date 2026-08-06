package disks_test

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

// TestSDKDiskCostFields verifies that provisioned-performance cost fields
// (diskIOPSReadWrite, diskMBpsReadWrite, tier, sku.tier) set on a real
// armcompute.DisksClient create round-trip back through Get.
func TestSDKDiskCostFields(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		VirtualMachines: cloudP.VirtualMachines,
		Disks:           cloudP.VirtualMachines,
	})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newDisksClient(t, ts)
	ctx := context.Background()

	createPoller, err := client.BeginCreateOrUpdate(ctx, "rg-1", "perf-disk-1",
		armcompute.Disk{
			Location: to.Ptr("eastus"),
			SKU:      &armcompute.DiskSKU{Name: to.Ptr(armcompute.DiskStorageAccountTypesPremiumV2LRS)},
			Properties: &armcompute.DiskProperties{
				CreationData:      &armcompute.CreationData{CreateOption: to.Ptr(armcompute.DiskCreateOptionEmpty)},
				DiskSizeGB:        to.Ptr[int32](256),
				DiskIOPSReadWrite: to.Ptr[int64](5000),
				DiskMBpsReadWrite: to.Ptr[int64](200),
				Tier:              to.Ptr("P10"),
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := createPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("create poll: %v", err)
	}

	got, err := client.Get(ctx, "rg-1", "perf-disk-1", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties == nil {
		t.Fatal("got.Properties is nil")
	}

	if got.Properties.DiskIOPSReadWrite == nil || *got.Properties.DiskIOPSReadWrite != 5000 {
		t.Errorf("diskIOPSReadWrite=%v want 5000", got.Properties.DiskIOPSReadWrite)
	}

	if got.Properties.DiskMBpsReadWrite == nil || *got.Properties.DiskMBpsReadWrite != 200 {
		t.Errorf("diskMBpsReadWrite=%v want 200", got.Properties.DiskMBpsReadWrite)
	}

	if got.Properties.Tier == nil || *got.Properties.Tier != "P10" {
		t.Errorf("properties.tier=%v want P10", got.Properties.Tier)
	}

	if got.SKU == nil || got.SKU.Tier == nil || *got.SKU.Tier != "P10" {
		t.Errorf("sku.tier=%v want P10", got.SKU)
	}
}
