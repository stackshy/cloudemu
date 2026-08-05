package virtualmachines_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// armOptions builds arm.ClientOptions pointed at the TLS test server. fakeCred
// is declared in sdk_roundtrip_test.go (same test package).
func armOptions(t *testing.T, ts *httptest.Server) *arm.ClientOptions {
	t.Helper()

	return &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud: cloud.Configuration{
				ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
				Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
					cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
				},
			},
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}
}

// TestSDKVMCostFields asserts priority/licenseType/osType survive a real
// armcompute VirtualMachinesClient create -> get round-trip.
func TestSDKVMCostFields(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{VirtualMachines: cloudP.VirtualMachines})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client, err := armcompute.NewVirtualMachinesClient("sub-1", fakeCred{}, armOptions(t, ts))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, "rg-1", "cost-vm", armcompute.VirtualMachine{
		Location: to.Ptr("eastus"),
		Zones:    []*string{to.Ptr("1")},
		Properties: &armcompute.VirtualMachineProperties{
			HardwareProfile: &armcompute.HardwareProfile{VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardD2SV3)},
			Priority:        to.Ptr(armcompute.VirtualMachinePriorityTypesSpot),
			LicenseType:     to.Ptr("Windows_Server"),
			StorageProfile: &armcompute.StorageProfile{
				OSDisk: &armcompute.OSDisk{
					OSType:       to.Ptr(armcompute.OperatingSystemTypesWindows),
					CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesFromImage),
				},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("create poll: %v", err)
	}

	got, err := client.Get(ctx, "rg-1", "cost-vm", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties == nil || got.Properties.Priority == nil ||
		*got.Properties.Priority != armcompute.VirtualMachinePriorityTypesSpot {
		t.Errorf("priority=%v want Spot", got.Properties.Priority)
	}

	if got.Properties.LicenseType == nil || *got.Properties.LicenseType != "Windows_Server" {
		t.Errorf("licenseType=%v want Windows_Server", got.Properties.LicenseType)
	}

	if got.Properties.StorageProfile == nil || got.Properties.StorageProfile.OSDisk == nil ||
		got.Properties.StorageProfile.OSDisk.OSType == nil ||
		*got.Properties.StorageProfile.OSDisk.OSType != armcompute.OperatingSystemTypesWindows {
		t.Errorf("osType did not round-trip: %+v", got.Properties.StorageProfile)
	}

	if len(got.Zones) != 1 || got.Zones[0] == nil || *got.Zones[0] != "1" {
		t.Errorf("zones=%v want [1]", got.Zones)
	}
}

// TestSDKVMSSCostFields asserts sku.capacity + virtualMachineProfile.priority
// survive a real armcompute VirtualMachineScaleSetsClient create -> get.
func TestSDKVMSSCostFields(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{VirtualMachines: cloudP.VirtualMachines})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client, err := armcompute.NewVirtualMachineScaleSetsClient("sub-1", fakeCred{}, armOptions(t, ts))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, "rg-1", "cost-vmss", armcompute.VirtualMachineScaleSet{
		Location: to.Ptr("eastus"),
		SKU: &armcompute.SKU{
			Name:     to.Ptr("Standard_D2s_v3"),
			Tier:     to.Ptr("Standard"),
			Capacity: to.Ptr[int64](3),
		},
		Properties: &armcompute.VirtualMachineScaleSetProperties{
			VirtualMachineProfile: &armcompute.VirtualMachineScaleSetVMProfile{
				Priority:    to.Ptr(armcompute.VirtualMachinePriorityTypesSpot),
				LicenseType: to.Ptr("Windows_Server"),
				StorageProfile: &armcompute.VirtualMachineScaleSetStorageProfile{
					OSDisk: &armcompute.VirtualMachineScaleSetOSDisk{
						OSType: to.Ptr(armcompute.OperatingSystemTypesWindows),
					},
				},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("VMSS BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("VMSS create poll: %v", err)
	}

	got, err := client.Get(ctx, "rg-1", "cost-vmss", nil)
	if err != nil {
		t.Fatalf("VMSS Get: %v", err)
	}

	if got.SKU == nil || got.SKU.Capacity == nil || *got.SKU.Capacity != 3 {
		t.Errorf("sku.capacity=%v want 3", got.SKU)
	}

	vp := got.Properties.VirtualMachineProfile
	if vp == nil || vp.Priority == nil || *vp.Priority != armcompute.VirtualMachinePriorityTypesSpot {
		t.Errorf("virtualMachineProfile.priority=%v want Spot", vp)
	}
}
