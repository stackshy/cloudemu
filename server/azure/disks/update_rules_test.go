package disks_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"

	"github.com/stackshy/cloudemu/v2/providers/azure/virtualmachines"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// diskVolumeID resolves an ARM disk name to the driver volume behind it.
func diskVolumeID(t *testing.T, vm *virtualmachines.Mock, name string) string {
	t.Helper()

	vols, err := vm.DescribeVolumes(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	for i := range vols {
		if vols[i].Tags["cloudemu:azureDiskName"] == name {
			return vols[i].ID
		}
	}

	t.Fatalf("no volume for disk %s", name)

	return ""
}

// attachToNewVM starts a VM and attaches the named disk to it at device.
func attachToNewVM(t *testing.T, vm *virtualmachines.Mock, disk, device string) string {
	t.Helper()

	ctx := context.Background()

	insts, err := vm.RunInstances(ctx, computedriver.InstanceConfig{ImageID: "img", InstanceType: "Standard_D2s_v3"}, 1)
	if err != nil {
		t.Fatal(err)
	}

	if err := vm.AttachVolume(ctx, diskVolumeID(t, vm, disk), insts[0].ID, device); err != nil {
		t.Fatal(err)
	}

	return insts[0].ID
}

func sizeUpdate(gb int32) armcompute.DiskUpdate {
	return armcompute.DiskUpdate{Properties: &armcompute.DiskUpdateProperties{DiskSizeGB: to.Ptr(gb)}}
}

func skuUpdate(sku armcompute.DiskStorageAccountTypes) armcompute.DiskUpdate {
	return armcompute.DiskUpdate{SKU: &armcompute.DiskSKU{Name: to.Ptr(sku)}}
}

func tierUpdate(tier string) armcompute.DiskUpdate {
	return armcompute.DiskUpdate{Properties: &armcompute.DiskUpdateProperties{Tier: to.Ptr(tier)}}
}

func diskSize(t *testing.T, client *armcompute.DisksClient, name string) int32 {
	t.Helper()

	got, err := client.Get(context.Background(), "rg-1", name, nil)
	if err != nil {
		t.Fatalf("Get %s: %v", name, err)
	}

	return *got.Properties.DiskSizeGB
}

// TestSDKDiskRePutCannotShrink checks that the grow-only rule lives in the
// provider: a CreateOrUpdate re-PUT on an existing disk cannot shrink it
// either (it used to answer 202 and store the smaller size).
func TestSDKDiskRePutCannotShrink(t *testing.T) {
	_, client := newUpdateTestServer(t)
	ctx := context.Background()

	createDiskForUpdate(t, client, "shrink-disk", armcompute.DiskStorageAccountTypesPremiumLRS)

	if _, err := beginUpdate(t, client, "shrink-disk", sizeUpdate(128)); err != nil {
		t.Fatalf("grow: %v", err)
	}

	_, err := client.BeginCreateOrUpdate(ctx, "rg-1", "shrink-disk", armcompute.Disk{
		Location: to.Ptr("eastus"),
		SKU:      &armcompute.DiskSKU{Name: to.Ptr(armcompute.DiskStorageAccountTypesPremiumLRS)},
		Properties: &armcompute.DiskProperties{
			CreationData: &armcompute.CreationData{CreateOption: to.Ptr(armcompute.DiskCreateOptionEmpty)},
			DiskSizeGB:   to.Ptr[int32](16),
		},
	}, nil)
	wantDiskErr(t, err, http.StatusBadRequest, "InvalidParameter")

	if got := diskSize(t, client, "shrink-disk"); got != 128 {
		t.Errorf("diskSizeGB=%d after a rejected shrink, want 128", got)
	}
}

// TestSDKDiskUpdateAttachedToRunningVM covers the attached-disk rules from
// Microsoft Learn (troubleshoot-disk-resize, expand-disks): an OS disk is only
// resized and any disk only converted while the VM is deallocated, a data disk
// can grow online, and a Standard/Premium disk of 4 TiB or less cannot grow
// past 4 TiB while attached.
func TestSDKDiskUpdateAttachedToRunningVM(t *testing.T) {
	_, client, vm := newUpdateTestEnv(t)
	ctx := context.Background()

	createDiskForUpdate(t, client, "os-disk", armcompute.DiskStorageAccountTypesPremiumLRS)
	createDiskForUpdate(t, client, "data-disk", armcompute.DiskStorageAccountTypesStandardLRS)

	osVM := attachToNewVM(t, vm, "os-disk", "osdisk")
	attachToNewVM(t, vm, "data-disk", "0")

	_, err := beginUpdate(t, client, "os-disk", sizeUpdate(128))
	wantDiskErr(t, err, http.StatusConflict, "OperationNotAllowed")

	_, err = beginUpdate(t, client, "os-disk", skuUpdate(armcompute.DiskStorageAccountTypesStandardSSDLRS))
	wantDiskErr(t, err, http.StatusConflict, "OperationNotAllowed")

	_, err = beginUpdate(t, client, "data-disk", sizeUpdate(8192))
	wantDiskErr(t, err, http.StatusConflict, "InvalidResizeForLargeDisks")

	if _, err := beginUpdate(t, client, "data-disk", sizeUpdate(128)); err != nil {
		t.Fatalf("online data-disk grow: %v", err)
	}

	if _, err := beginUpdate(t, client, "os-disk", armcompute.DiskUpdate{
		Tags: map[string]*string{"k": to.Ptr("v")},
	}); err != nil {
		t.Fatalf("tags on an attached disk: %v", err)
	}

	if got := diskSize(t, client, "os-disk"); got != 64 {
		t.Errorf("os-disk diskSizeGB=%d after rejected resize, want 64", got)
	}

	if err := vm.Deallocate(ctx, osVM); err != nil {
		t.Fatal(err)
	}

	if _, err := beginUpdate(t, client, "os-disk", sizeUpdate(128)); err != nil {
		t.Fatalf("resize after deallocate: %v", err)
	}

	if _, err := beginUpdate(t, client, "os-disk", skuUpdate(armcompute.DiskStorageAccountTypesStandardSSDLRS)); err != nil {
		t.Fatalf("sku change after deallocate: %v", err)
	}
}

// TestSDKDiskUpdateWithActiveSAS checks that an active beginGetAccess SAS
// blocks a resize (ChangeDiskSizeWhileActiveSasNotAllowed) and a conversion,
// while a tags-only update still goes through, and that endGetAccess lifts it.
func TestSDKDiskUpdateWithActiveSAS(t *testing.T) {
	_, client := newUpdateTestServer(t)
	ctx := context.Background()

	createDiskForUpdate(t, client, "sas-upd", armcompute.DiskStorageAccountTypesStandardLRS)

	grant, err := client.BeginGrantAccess(ctx, "rg-1", "sas-upd", armcompute.GrantAccessData{
		Access: to.Ptr(armcompute.AccessLevelRead), DurationInSeconds: to.Ptr[int32](3600),
	}, nil)
	if err != nil {
		t.Fatalf("BeginGrantAccess: %v", err)
	}

	if _, err := grant.PollUntilDone(ctx, fastPoll); err != nil {
		t.Fatalf("grant poll: %v", err)
	}

	_, err = beginUpdate(t, client, "sas-upd", sizeUpdate(256))
	wantDiskErr(t, err, http.StatusConflict, "ChangeDiskSizeWhileActiveSasNotAllowed")

	_, err = beginUpdate(t, client, "sas-upd", skuUpdate(armcompute.DiskStorageAccountTypesPremiumLRS))
	wantDiskErr(t, err, http.StatusConflict, "OperationNotAllowed")

	if _, err := beginUpdate(t, client, "sas-upd", armcompute.DiskUpdate{
		Tags: map[string]*string{"k": to.Ptr("v")},
	}); err != nil {
		t.Fatalf("tags with an active SAS: %v", err)
	}

	revoke, err := client.BeginRevokeAccess(ctx, "rg-1", "sas-upd", nil)
	if err != nil {
		t.Fatalf("BeginRevokeAccess: %v", err)
	}

	if _, err := revoke.PollUntilDone(ctx, fastPoll); err != nil {
		t.Fatalf("revoke poll: %v", err)
	}

	if _, err := beginUpdate(t, client, "sas-upd", sizeUpdate(256)); err != nil {
		t.Fatalf("resize after revoke: %v", err)
	}
}

// TestSDKDiskUpdatePerformanceTier covers the Premium SSD performance tier
// rules (Microsoft Learn, disks-change-performance) and the SKU conversion
// rules (disks-convert-types).
func TestSDKDiskUpdatePerformanceTier(t *testing.T) {
	_, client := newUpdateTestServer(t)

	createDiskForUpdate(t, client, "tier-disk", armcompute.DiskStorageAccountTypesStandardSSDLRS)
	createDiskForUpdate(t, client, "ultra-disk", armcompute.DiskStorageAccountTypesUltraSSDLRS)

	// A tier only applies to Premium SSD.
	_, err := beginUpdate(t, client, "tier-disk", tierUpdate("P30"))
	wantDiskErr(t, err, http.StatusBadRequest, "InvalidParameter")

	_, err = beginUpdate(t, client, "ultra-disk", tierUpdate("P50"))
	wantDiskErr(t, err, http.StatusBadRequest, "InvalidParameter")

	// Moving to Premium SSD sets the baseline tier for 64 GiB (P6).
	got, err := beginUpdate(t, client, "tier-disk", skuUpdate(armcompute.DiskStorageAccountTypesPremiumLRS))
	if err != nil {
		t.Fatalf("to Premium_LRS: %v", err)
	}

	wantTier(t, got, "P6")

	for _, tc := range []struct{ tier, why string }{
		{"P4", "below the P6 baseline"},
		{"P70", "P60-P80 need a disk above 4 TiB"},
		{"Premium", "sku.tier value, not a performance tier"},
	} {
		t.Run(tc.why, func(t *testing.T) {
			_, err := beginUpdate(t, client, "tier-disk", tierUpdate(tc.tier))
			wantDiskErr(t, err, http.StatusBadRequest, "InvalidParameter")
		})
	}

	got, err = beginUpdate(t, client, "tier-disk", tierUpdate("P50"))
	if err != nil {
		t.Fatalf("tier P50: %v", err)
	}

	wantTier(t, got, "P50")

	// sku.tier is read-only: sending it does not change the performance tier.
	got, err = beginUpdate(t, client, "tier-disk", armcompute.DiskUpdate{
		SKU: &armcompute.DiskSKU{Name: to.Ptr(armcompute.DiskStorageAccountTypesPremiumLRS), Tier: to.Ptr("Premium")},
	})
	if err != nil {
		t.Fatalf("sku.tier: %v", err)
	}

	wantTier(t, got, "P50")

	// Moving off Premium SSD clears the tier.
	got, err = beginUpdate(t, client, "tier-disk", skuUpdate(armcompute.DiskStorageAccountTypesStandardSSDLRS))
	if err != nil {
		t.Fatalf("to StandardSSD_LRS: %v", err)
	}

	if got.Properties.Tier != nil {
		t.Errorf("tier=%s after moving off Premium SSD, want omitted", *got.Properties.Tier)
	}

	// Ultra Disk is neither a conversion source nor a target.
	_, err = beginUpdate(t, client, "tier-disk", skuUpdate(armcompute.DiskStorageAccountTypesUltraSSDLRS))
	wantDiskErr(t, err, http.StatusBadRequest, "InvalidParameter")

	_, err = beginUpdate(t, client, "ultra-disk", skuUpdate(armcompute.DiskStorageAccountTypesPremiumLRS))
	wantDiskErr(t, err, http.StatusBadRequest, "InvalidParameter")
}

func wantTier(t *testing.T, d armcompute.Disk, tier string) {
	t.Helper()

	if d.Properties.Tier == nil || *d.Properties.Tier != tier {
		t.Errorf("tier=%v want %s", d.Properties.Tier, tier)
	}
}

// TestSDKDiskConcurrentPatchesBothLand runs a size PATCH and a tags PATCH on
// the same disks at once. The merge happens in the provider under the store
// lock, so neither overwrites the other with a stale snapshot.
func TestSDKDiskConcurrentPatchesBothLand(t *testing.T) {
	_, client := newUpdateTestServer(t)

	const disks = 20

	for i := range disks {
		createDiskForUpdate(t, client, fmt.Sprintf("race-%d", i), armcompute.DiskStorageAccountTypesPremiumLRS)
	}

	var wg sync.WaitGroup

	for i := range disks {
		name := fmt.Sprintf("race-%d", i)

		wg.Add(2)

		go func() {
			defer wg.Done()

			if _, err := beginUpdate(t, client, name, sizeUpdate(128)); err != nil {
				t.Errorf("size PATCH %s: %v", name, err)
			}
		}()

		go func() {
			defer wg.Done()

			if _, err := beginUpdate(t, client, name, armcompute.DiskUpdate{
				Tags: map[string]*string{"owner": to.Ptr("bob")},
			}); err != nil {
				t.Errorf("tags PATCH %s: %v", name, err)
			}
		}()
	}

	wg.Wait()

	for i := range disks {
		name := fmt.Sprintf("race-%d", i)

		got, err := client.Get(context.Background(), "rg-1", name, nil)
		if err != nil {
			t.Fatalf("Get %s: %v", name, err)
		}

		if *got.Properties.DiskSizeGB != 128 || got.Tags["owner"] == nil {
			t.Errorf("%s: size=%d tags=%v, want both PATCHes applied", name, *got.Properties.DiskSizeGB, got.Tags)
		}
	}
}

// TestDiskPatchMalformedBody checks a PATCH body that is not JSON is the 400
// InvalidRequestContent ARM returns, and leaves the disk alone.
func TestDiskPatchMalformedBody(t *testing.T) {
	ts, client := newUpdateTestServer(t)

	createDiskForUpdate(t, client, "bad-body", armcompute.DiskStorageAccountTypesPremiumLRS)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPatch,
		ts.URL+diskPath("rg-1", "bad-body")+wireAPIVersion, strings.NewReader(`{"properties":`))
	if err != nil {
		t.Fatal(err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "InvalidRequestContent") {
		t.Errorf("status=%d body=%s, want 400 InvalidRequestContent", resp.StatusCode, body)
	}

	if got := diskSize(t, client, "bad-body"); got != 64 {
		t.Errorf("diskSizeGB=%d, want 64", got)
	}
}
