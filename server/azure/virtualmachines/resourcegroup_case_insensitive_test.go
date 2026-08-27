package virtualmachines_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestVMResourceGroupCaseInsensitive verifies that ARM's case-insensitive
// resource-group semantics hold on GET and LIST: a VM created under "rg-1" is
// resolvable by a differently-cased "RG-1" path, and its unmodeled overlay
// properties (here additionalCapabilities, which the handler does not model)
// survive the differently-cased read. Before the fix, findByName/list compared
// the resource group with an exact "!=", so the upper-cased read 404'd; the
// overlay was keyed by the request-path casing, so it also missed.
func TestVMResourceGroupCaseInsensitive(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{VirtualMachines: cloudP.VirtualMachines})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newSDKClient(t, ts)
	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, "rg-1", "case-vm",
		armcompute.VirtualMachine{
			Location: to.Ptr("eastus"),
			Properties: &armcompute.VirtualMachineProperties{
				HardwareProfile: &armcompute.HardwareProfile{
					VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardD2SV3),
				},
				// additionalCapabilities is not modeled by the handler, so it is
				// captured by the property overlay — the ideal probe for whether
				// a differently-cased read still resolves the overlay entry.
				AdditionalCapabilities: &armcompute.AdditionalCapabilities{
					HibernationEnabled: to.Ptr(true),
				},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := pollUntilDone(ctx, poller); err != nil {
		t.Fatalf("CreateOrUpdate poll: %v", err)
	}

	// GET with an upper-cased resource group must return the VM (case-insensitive
	// resource-group lookup), not a spurious 404.
	got, err := client.Get(ctx, "RG-1", "case-vm", nil)
	if err != nil {
		t.Fatalf("Get with upper-cased RG: %v", err)
	}

	if got.Name == nil || *got.Name != "case-vm" {
		t.Fatalf("got.Name=%v want case-vm", got.Name)
	}

	// The unmodeled overlay property must survive the differently-cased read.
	if got.Properties == nil || got.Properties.AdditionalCapabilities == nil ||
		got.Properties.AdditionalCapabilities.HibernationEnabled == nil ||
		!*got.Properties.AdditionalCapabilities.HibernationEnabled {
		t.Fatalf("additionalCapabilities.hibernationEnabled not echoed on upper-cased GET: %+v",
			got.Properties)
	}

	// GET with a differently-cased VM name must also resolve (names are
	// case-insensitive in ARM).
	if _, err := client.Get(ctx, "rg-1", "CASE-VM", nil); err != nil {
		t.Fatalf("Get with upper-cased VM name: %v", err)
	}

	// LIST scoped by an upper-cased resource group must return the VM.
	pager := client.NewListPager("RG-1", nil)

	found := false

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		for _, vm := range page.Value {
			if vm.Name != nil && *vm.Name == "case-vm" {
				found = true
			}
		}
	}

	if !found {
		t.Error("List with upper-cased RG did not return case-vm")
	}
}

// TestScaleSetResourceNameCaseInsensitive verifies a scale set created as
// "vmss-1" is resolvable by a differently-cased "VMSS-1" GET. Before the fix,
// getScaleSet compared the name with an exact "==", spuriously 404ing.
func TestScaleSetResourceNameCaseInsensitive(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{VirtualMachines: cloudP.VirtualMachines})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client, err := armcompute.NewVirtualMachineScaleSetsClient("sub-1", fakeCred{}, sdkClientOptions(ts))
	if err != nil {
		t.Fatalf("NewVirtualMachineScaleSetsClient: %v", err)
	}

	ctx := context.Background()
	createVMSS(ctx, t, client, "rg-1", "vmss-1")

	// GET by a differently-cased scale-set name must resolve.
	got, err := client.Get(ctx, "rg-1", "VMSS-1", nil)
	if err != nil {
		t.Fatalf("Get scale set with upper-cased name: %v", err)
	}

	if got.Name == nil || !strings.EqualFold(*got.Name, "vmss-1") {
		t.Fatalf("got.Name=%v want vmss-1", got.Name)
	}

	// GET by a differently-cased resource group must also resolve.
	if _, err := client.Get(ctx, "RG-1", "vmss-1", nil); err != nil {
		t.Fatalf("Get scale set with upper-cased RG: %v", err)
	}
}
