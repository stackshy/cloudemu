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

// TestSDKVMPatchUpdateAppliesVMSizeAndReplacesTags is the regression test for
// the PATCH Update fix: a real armcompute BeginUpdate that supplies
// hardwareProfile.vmSize (a resize) and a tags map must take effect — Get shows
// the new size and the new tag set. Unlike a generic RFC 7386 merge-patch, real
// Azure Compute's PATCH tags is a full replace: a tag set at create and omitted
// from the PATCH body does NOT survive (this is a well-documented Azure quirk —
// see providers/azure/sqlvirtualmachine's UpdateTags for the sibling case).
// Before the underlying fix update() only reconciled data disks and silently
// dropped vmSize/tags entirely.
func TestSDKVMPatchUpdateAppliesVMSizeAndReplacesTags(t *testing.T) {
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

	// PATCH: resize to D4s_v3 and set a "team" tag. "env" (set at create) is not
	// re-sent, so real Azure's tags-replace semantics drop it.
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

	// New tag applied, but the tag set was replaced wholesale: the create-time
	// "env" tag, omitted from the PATCH body, does not survive.
	if v := tagVal(got.Tags, "team"); v != "blue" {
		t.Errorf("tag team=%q, want blue", v)
	}

	if _, ok := got.Tags["env"]; ok {
		t.Errorf("tag env=%q survived PATCH; real Azure PATCH tags replaces the set wholesale", tagVal(got.Tags, "env"))
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
