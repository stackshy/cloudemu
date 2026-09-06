package virtualmachines_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestSDKVMPlanRoundTrip verifies that a VM's top-level plan block (the
// marketplace purchase plan, armcompute.VirtualMachine.Plan) survives a
// create → get round-trip. plan is a sibling of properties, so the generic
// property overlay cannot recover it — the handler must model it explicitly.
// Without that, a marketplace-image VM read back through the Azure SDK (or
// azurerm's Read) sees an empty plan and drifts/replaces on every plan.
func TestSDKVMPlanRoundTrip(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{VirtualMachines: cloudP.VirtualMachines})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newSDKClient(t, ts)
	ctx := context.Background()

	wantPlan := &armcompute.Plan{
		Name:      to.Ptr("cloud-instance"),
		Publisher: to.Ptr("cloudvendor"),
		Product:   to.Ptr("vm-image"),
	}

	poller, err := client.BeginCreateOrUpdate(ctx, "rg-plan", "mkt-vm",
		armcompute.VirtualMachine{
			Location: to.Ptr("eastus"),
			Plan:     wantPlan,
			Properties: &armcompute.VirtualMachineProperties{
				HardwareProfile: &armcompute.HardwareProfile{
					VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardF2),
				},
				StorageProfile: &armcompute.StorageProfile{
					ImageReference: &armcompute.ImageReference{
						Publisher: to.Ptr("cloudvendor"),
						Offer:     to.Ptr("vm-image"),
						SKU:       to.Ptr("cloud-instance"),
						Version:   to.Ptr("latest"),
					},
					OSDisk: &armcompute.OSDisk{
						Name:         to.Ptr("mkt-vm_osdisk"),
						CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesFromImage),
						OSType:       to.Ptr(armcompute.OperatingSystemTypesLinux),
						ManagedDisk: &armcompute.ManagedDiskParameters{
							StorageAccountType: to.Ptr(armcompute.StorageAccountTypesStandardLRS),
						},
					},
				},
				OSProfile: &armcompute.OSProfile{
					ComputerName:  to.Ptr("mkt-vm"),
					AdminUsername: to.Ptr("azureuser"),
					LinuxConfiguration: &armcompute.LinuxConfiguration{
						DisablePasswordAuthentication: to.Ptr(false),
					},
				},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	created, err := pollUntilDone(ctx, poller)
	if err != nil {
		t.Fatalf("CreateOrUpdate poll: %v", err)
	}

	assertPlan(t, "create", created.Plan, wantPlan)

	createdVMID := ""
	if created.Properties != nil && created.Properties.VMID != nil {
		createdVMID = *created.Properties.VMID
	}

	if createdVMID == "" {
		t.Fatal("create: properties.vmId is empty, want a stable GUID")
	}

	got, err := client.Get(ctx, "rg-plan", "mkt-vm", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	assertPlan(t, "get", got.Plan, wantPlan)

	// vmId must be a stable GUID: a value that changes per GET makes Terraform's
	// computed virtual_machine_id drift on every plan.
	if got.Properties == nil || got.Properties.VMID == nil || *got.Properties.VMID != createdVMID {
		t.Errorf("get vmId=%v, want stable %q", got.Properties.VMID, createdVMID)
	}

	assertGetRoundTrip(t, got.VirtualMachine)
}

// assertGetRoundTrip checks the storageProfile / osProfile fields that
// Terraform reads back and drifts on when the emulator drops them: the OS
// disk's managedDisk.storageAccountType, the imageReference, and the explicit
// linuxConfiguration.disablePasswordAuthentication=false (a zero scalar the
// overlay must still preserve because the whole osProfile is unmodeled).
func assertGetRoundTrip(t *testing.T, vm armcompute.VirtualMachine) {
	t.Helper()

	sp := vm.Properties.StorageProfile
	if sp == nil || sp.OSDisk == nil || sp.OSDisk.ManagedDisk == nil ||
		sp.OSDisk.ManagedDisk.StorageAccountType == nil ||
		*sp.OSDisk.ManagedDisk.StorageAccountType != armcompute.StorageAccountTypesStandardLRS {
		t.Errorf("get osDisk.managedDisk.storageAccountType did not round-trip: %+v", sp)
	}

	if sp == nil || sp.ImageReference == nil || sp.ImageReference.SKU == nil ||
		*sp.ImageReference.SKU != "cloud-instance" {
		t.Errorf("get storageProfile.imageReference did not round-trip: %+v", sp)
	}

	op := vm.Properties.OSProfile
	if op == nil || op.LinuxConfiguration == nil ||
		op.LinuxConfiguration.DisablePasswordAuthentication == nil ||
		*op.LinuxConfiguration.DisablePasswordAuthentication != false {
		t.Errorf("get osProfile.linuxConfiguration.disablePasswordAuthentication=false did not round-trip: %+v", op)
	}
}

func assertPlan(t *testing.T, stage string, got, want *armcompute.Plan) {
	t.Helper()

	if got == nil {
		t.Fatalf("%s: plan is nil, want %+v", stage, want)
	}

	if derefStr(got.Name) != derefStr(want.Name) ||
		derefStr(got.Publisher) != derefStr(want.Publisher) ||
		derefStr(got.Product) != derefStr(want.Product) {
		t.Errorf("%s: plan=%+v, want name=%s publisher=%s product=%s",
			stage, got, derefStr(want.Name), derefStr(want.Publisher), derefStr(want.Product))
	}
}
