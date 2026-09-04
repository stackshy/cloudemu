package virtualmachines_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestSDKVMSSInstanceOrchestration drives the real armcompute
// VirtualMachineScaleSetVMsClient against an in-process cloudemu server: a
// scale set created at capacity N materializes N addressable instances that can
// be listed, fetched, powered off, and deleted — a delete dropping one so the
// list returns N-1.
func TestSDKVMSSInstanceOrchestration(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{VirtualMachines: cloudP.VirtualMachines})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	ssClient, err := armcompute.NewVirtualMachineScaleSetsClient("sub-1", fakeCred{}, sdkClientOptions(ts))
	if err != nil {
		t.Fatal(err)
	}

	vmClient, err := armcompute.NewVirtualMachineScaleSetVMsClient("sub-1", fakeCred{}, sdkClientOptions(ts))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	const (
		rg       = "rg-1"
		name     = "orch-vmss"
		capacity = 3
	)

	createVMSSWithCapacity(ctx, t, ssClient, rg, name, capacity)

	// LIST returns the materialized instances, one per capacity unit.
	ids := listVMSSVMInstanceIDs(ctx, t, vmClient, rg, name)
	if len(ids) != capacity {
		t.Fatalf("List: got %d instances, want %d", len(ids), capacity)
	}

	// GET a single instance resolves it and echoes its ordinal + composite name.
	got, err := vmClient.Get(ctx, rg, name, ids[0], nil)
	if err != nil {
		t.Fatalf("Get instance %q: %v", ids[0], err)
	}

	if got.InstanceID == nil || *got.InstanceID != ids[0] {
		t.Fatalf("Get instanceId = %v, want %q", got.InstanceID, ids[0])
	}

	if got.Name == nil || *got.Name != name+"_"+ids[0] {
		t.Fatalf("Get name = %v, want %q", got.Name, name+"_"+ids[0])
	}

	// POWER OFF one instance; its instanceView must reflect PowerState/stopped.
	powerOffVMSSVM(ctx, t, vmClient, rg, name, ids[0])

	if state := instanceViewPowerState(ctx, t, vmClient, rg, name, ids[0]); state != "PowerState/stopped" {
		t.Fatalf("after powerOff, power state = %q, want PowerState/stopped", state)
	}

	// DELETE one instance; the list must then return one fewer.
	deleteVMSSVM(ctx, t, vmClient, rg, name, ids[1])

	after := listVMSSVMInstanceIDs(ctx, t, vmClient, rg, name)
	if len(after) != capacity-1 {
		t.Fatalf("List after delete: got %d instances, want %d", len(after), capacity-1)
	}

	for _, id := range after {
		if id == ids[1] {
			t.Fatalf("deleted instance %q still present after delete", ids[1])
		}
	}
}

func createVMSSWithCapacity(
	ctx context.Context, t *testing.T, client *armcompute.VirtualMachineScaleSetsClient, rg, name string, capacity int64,
) {
	t.Helper()

	poller, err := client.BeginCreateOrUpdate(ctx, rg, name, armcompute.VirtualMachineScaleSet{
		Location: to.Ptr("westus2"),
		SKU: &armcompute.SKU{
			Name:     to.Ptr("Standard_D2s_v3"),
			Tier:     to.Ptr("Standard"),
			Capacity: to.Ptr(capacity),
		},
	}, nil)
	if err != nil {
		t.Fatalf("VMSS BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("VMSS create poll: %v", err)
	}
}

func listVMSSVMInstanceIDs(
	ctx context.Context, t *testing.T, client *armcompute.VirtualMachineScaleSetVMsClient, rg, name string,
) []string {
	t.Helper()

	var ids []string

	pager := client.NewListPager(rg, name, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("VMSS VM list: %v", err)
		}

		for _, vm := range page.Value {
			if vm.InstanceID != nil {
				ids = append(ids, *vm.InstanceID)
			}
		}
	}

	return ids
}

func powerOffVMSSVM(
	ctx context.Context, t *testing.T, client *armcompute.VirtualMachineScaleSetVMsClient, rg, name, instanceID string,
) {
	t.Helper()

	poller, err := client.BeginPowerOff(ctx, rg, name, instanceID, nil)
	if err != nil {
		t.Fatalf("BeginPowerOff %q: %v", instanceID, err)
	}

	if _, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("PowerOff poll: %v", err)
	}
}

func deleteVMSSVM(
	ctx context.Context, t *testing.T, client *armcompute.VirtualMachineScaleSetVMsClient, rg, name, instanceID string,
) {
	t.Helper()

	poller, err := client.BeginDelete(ctx, rg, name, instanceID, nil)
	if err != nil {
		t.Fatalf("BeginDelete %q: %v", instanceID, err)
	}

	if _, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("Delete poll: %v", err)
	}
}

func instanceViewPowerState(
	ctx context.Context, t *testing.T, client *armcompute.VirtualMachineScaleSetVMsClient, rg, name, instanceID string,
) string {
	t.Helper()

	view, err := client.GetInstanceView(ctx, rg, name, instanceID, nil)
	if err != nil {
		t.Fatalf("GetInstanceView %q: %v", instanceID, err)
	}

	for _, s := range view.Statuses {
		if s.Code != nil && strings.HasPrefix(*s.Code, "PowerState/") {
			return *s.Code
		}
	}

	return ""
}
