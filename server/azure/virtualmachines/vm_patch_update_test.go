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

// TestSDKVMPatchUpdateAppliesVMSizeAndMergesTags is the regression test for the
// PATCH Update fix: a real armcompute BeginUpdate that supplies
// hardwareProfile.vmSize (a resize) and a new tag must take effect — Get shows
// the new size and the added tag — while a tag the PATCH omitted (set at create)
// is preserved (ARM PATCH is RFC 7386 merge-patch). Before the fix update() only
// reconciled data disks and silently dropped vmSize/tags.
func TestSDKVMPatchUpdateAppliesVMSizeAndMergesTags(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{VirtualMachines: cloudP.VirtualMachines})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newSDKClient(t, ts)
	ctx := context.Background()

	createPoller, err := client.BeginCreateOrUpdate(ctx, "rg-1", "vm-patch",
		armcompute.VirtualMachine{
			Location: to.Ptr("eastus"),
			Tags:     map[string]*string{"env": to.Ptr("prod")},
			Properties: &armcompute.VirtualMachineProperties{
				HardwareProfile: &armcompute.HardwareProfile{
					VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardD2SV3),
				},
				OSProfile: &armcompute.OSProfile{
					ComputerName:  to.Ptr("vm-patch"),
					AdminUsername: to.Ptr("azureuser"),
				},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := pollUntilDone(ctx, createPoller); err != nil {
		t.Fatalf("CreateOrUpdate poll: %v", err)
	}

	// PATCH: resize to D4s_v3 and add a "team" tag. Neither the size nor the tag
	// were touched by the create; "env" is not re-sent (merge-patch must keep it).
	updatePoller, err := client.BeginUpdate(ctx, "rg-1", "vm-patch",
		armcompute.VirtualMachineUpdate{
			Tags: map[string]*string{"team": to.Ptr("blue")},
			Properties: &armcompute.VirtualMachineProperties{
				HardwareProfile: &armcompute.HardwareProfile{
					VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardD4SV3),
				},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}

	if _, err := updatePoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("Update poll: %v", err)
	}

	got, err := client.Get(ctx, "rg-1", "vm-patch", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Resize applied.
	if got.Properties == nil || got.Properties.HardwareProfile == nil || got.Properties.HardwareProfile.VMSize == nil ||
		*got.Properties.HardwareProfile.VMSize != armcompute.VirtualMachineSizeTypesStandardD4SV3 {
		t.Errorf("vmSize=%v, want Standard_D4s_v3", vmSizeOf(got.VirtualMachine))
	}

	// New tag added AND pre-existing tag preserved (merge, not replace).
	if v := tagVal(got.Tags, "team"); v != "blue" {
		t.Errorf("tag team=%q, want blue", v)
	}

	if v := tagVal(got.Tags, "env"); v != "prod" {
		t.Errorf("tag env=%q, want prod (PATCH must not drop an omitted existing tag)", v)
	}
}

func vmSizeOf(vm armcompute.VirtualMachine) string {
	if vm.Properties == nil || vm.Properties.HardwareProfile == nil || vm.Properties.HardwareProfile.VMSize == nil {
		return "<nil>"
	}

	return string(*vm.Properties.HardwareProfile.VMSize)
}

func tagVal(tags map[string]*string, key string) string {
	v, ok := tags[key]
	if !ok || v == nil {
		return ""
	}

	return *v
}
