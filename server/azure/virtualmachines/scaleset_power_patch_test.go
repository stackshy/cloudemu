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

// TestSDKVMSSWholeSetPower drives the real armcompute VirtualMachineScaleSetsClient
// whole-set power actions against an in-process cloudemu server: BeginPowerOff /
// BeginStart / BeginDeallocate / BeginRestart on the scale set must transition
// every materialized instance's power state, observable via each instance's
// instanceView.
func TestSDKVMSSWholeSetPower(t *testing.T) {
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
		name     = "power-vmss"
		capacity = 3
	)

	createVMSSWithCapacity(ctx, t, ssClient, rg, name, capacity)
	ids := listVMSSVMInstanceIDs(ctx, t, vmClient, rg, name)

	// PowerOff the whole set → every instance stopped.
	poller, err := ssClient.BeginPowerOff(ctx, rg, name, nil)
	if err != nil {
		t.Fatalf("BeginPowerOff: %v", err)
	}

	pollVMSSDone(ctx, t, poller)
	assertAllVMSSPower(ctx, t, vmClient, rg, name, ids, "PowerState/stopped")

	// Start the whole set → every instance running again.
	startPoller, err := ssClient.BeginStart(ctx, rg, name, nil)
	if err != nil {
		t.Fatalf("BeginStart: %v", err)
	}

	pollVMSSDone(ctx, t, startPoller)
	assertAllVMSSPower(ctx, t, vmClient, rg, name, ids, "PowerState/running")

	// Deallocate the whole set → every instance deallocated.
	deallocPoller, err := ssClient.BeginDeallocate(ctx, rg, name, nil)
	if err != nil {
		t.Fatalf("BeginDeallocate: %v", err)
	}

	pollVMSSDone(ctx, t, deallocPoller)
	assertAllVMSSPower(ctx, t, vmClient, rg, name, ids, "PowerState/deallocated")

	// Restart the whole set → every instance running.
	restartPoller, err := ssClient.BeginRestart(ctx, rg, name, nil)
	if err != nil {
		t.Fatalf("BeginRestart: %v", err)
	}

	pollVMSSDone(ctx, t, restartPoller)
	assertAllVMSSPower(ctx, t, vmClient, rg, name, ids, "PowerState/running")

	// PowerOff then Reimage the whole set → reimage settles every instance back
	// to running, like start/restart.
	offPoller, err := ssClient.BeginPowerOff(ctx, rg, name, nil)
	if err != nil {
		t.Fatalf("BeginPowerOff before reimage: %v", err)
	}

	pollVMSSDone(ctx, t, offPoller)
	assertAllVMSSPower(ctx, t, vmClient, rg, name, ids, "PowerState/stopped")

	reimagePoller, err := ssClient.BeginReimage(ctx, rg, name, nil)
	if err != nil {
		t.Fatalf("BeginReimage: %v", err)
	}

	pollVMSSDone(ctx, t, reimagePoller)
	assertAllVMSSPower(ctx, t, vmClient, rg, name, ids, "PowerState/running")
}

// TestSDKVMSSWholeSetPowerSubset targets a single instance via the instanceIds
// body: only the named instance transitions, the rest stay running.
func TestSDKVMSSWholeSetPowerSubset(t *testing.T) {
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
		name     = "subset-vmss"
		capacity = 3
	)

	createVMSSWithCapacity(ctx, t, ssClient, rg, name, capacity)
	ids := listVMSSVMInstanceIDs(ctx, t, vmClient, rg, name)

	poller, err := ssClient.BeginPowerOff(ctx, rg, name, &armcompute.VirtualMachineScaleSetsClientBeginPowerOffOptions{
		VMInstanceIDs: &armcompute.VirtualMachineScaleSetVMInstanceIDs{InstanceIDs: []*string{to.Ptr(ids[0])}},
	})
	if err != nil {
		t.Fatalf("BeginPowerOff subset: %v", err)
	}

	pollVMSSDone(ctx, t, poller)

	for _, id := range ids {
		want := "PowerState/running"
		if id == ids[0] {
			want = "PowerState/stopped"
		}

		if got := instanceViewPowerState(ctx, t, vmClient, rg, name, id); got != want {
			t.Fatalf("instance %q power = %q, want %q", id, got, want)
		}
	}
}

// TestSDKVMSSUpdate drives the real BeginUpdate (PATCH): a scale set created
// with one tag and capacity 2 is patched with a different tag and capacity 4.
// ARM resource-level PATCH replaces the tag set wholesale, so the response must
// carry only the new tag (the pre-existing one dropped) plus the new capacity,
// and the set must materialize 4 instances.
func TestSDKVMSSUpdate(t *testing.T) {
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
		rg   = "rg-1"
		name = "patch-vmss"
	)

	createPoller, err := ssClient.BeginCreateOrUpdate(ctx, rg, name, armcompute.VirtualMachineScaleSet{
		Location: to.Ptr("westus2"),
		Tags:     map[string]*string{"env": to.Ptr("dev")},
		SKU:      &armcompute.SKU{Name: to.Ptr("Standard_D2s_v3"), Capacity: to.Ptr[int64](2)},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err = createPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("create poll: %v", err)
	}

	updatePoller, err := ssClient.BeginUpdate(ctx, rg, name, armcompute.VirtualMachineScaleSetUpdate{
		Tags: map[string]*string{"team": to.Ptr("platform")},
		SKU:  &armcompute.SKU{Capacity: to.Ptr[int64](4)},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}

	res, err := updatePoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	if err != nil {
		t.Fatalf("update poll: %v", err)
	}

	if _, ok := res.Tags["env"]; ok {
		t.Fatalf("PATCH must replace the tag set wholesale, but pre-existing tag env survived: got %v", res.Tags)
	}

	if v := res.Tags["team"]; v == nil || *v != "platform" {
		t.Fatalf("PATCH did not apply new tag team: got %v", res.Tags)
	}

	if res.SKU == nil || res.SKU.Capacity == nil || *res.SKU.Capacity != 4 {
		t.Fatalf("PATCH capacity not applied: got %v", res.SKU)
	}

	if ids := listVMSSVMInstanceIDs(ctx, t, vmClient, rg, name); len(ids) != 4 {
		t.Fatalf("after capacity PATCH: got %d instances, want 4", len(ids))
	}
}

// TestSDKVMSSPowerAndPatchMissing confirms a whole-set power action and a PATCH
// against a non-existent scale set both surface 404.
func TestSDKVMSSPowerAndPatchMissing(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{VirtualMachines: cloudP.VirtualMachines})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	ssClient, err := armcompute.NewVirtualMachineScaleSetsClient("sub-1", fakeCred{}, sdkClientOptions(ts))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	if _, err = ssClient.BeginStart(ctx, "rg-x", "no-such-vmss", nil); !isNotFound(err) {
		t.Fatalf("BeginStart missing set: got %v, want 404", err)
	}

	if _, err = ssClient.BeginUpdate(ctx, "rg-x", "no-such-vmss", armcompute.VirtualMachineScaleSetUpdate{
		Tags: map[string]*string{"a": to.Ptr("b")},
	}, nil); !isNotFound(err) {
		t.Fatalf("BeginUpdate missing set: got %v, want 404", err)
	}
}

// pollVMSSDone drives any VMSS operation poller to completion at test speed.
func pollVMSSDone[T any](ctx context.Context, t *testing.T, poller *runtime.Poller[T]) {
	t.Helper()

	if _, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("poll: %v", err)
	}
}

// assertAllVMSSPower checks that every listed instance reports the wanted power
// state code via its instanceView.
func assertAllVMSSPower(
	ctx context.Context, t *testing.T, client *armcompute.VirtualMachineScaleSetVMsClient,
	rg, name string, ids []string, want string,
) {
	t.Helper()

	for _, id := range ids {
		if got := instanceViewPowerState(ctx, t, client, rg, name, id); got != want {
			t.Fatalf("instance %q power = %q, want %q", id, got, want)
		}
	}
}
