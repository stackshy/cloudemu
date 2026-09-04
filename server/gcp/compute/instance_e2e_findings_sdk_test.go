package compute_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"cloud.google.com/go/compute/apiv1/computepb"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestSDKDiskResizeReflectedOnInstance guards a real-user e2e finding: after
// disks.resize grows a boot/data disk, a subsequent instances.get must report
// the disk's CURRENT size in disks[].diskSizeGb, not the size it had at
// instance-insert time (real GCP's instance view is live, not a snapshot).
func TestSDKDiskResizeReflectedOnInstance(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Compute: cloudP.GCE})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	instances := newSDKInstancesClient(t, ts)
	disks := newDisksSDKClient(t, ts)
	ctx := context.Background()

	name := "resize-reflect-vm"
	mustInsert(t, instances, testZone, &computepb.Instance{
		Name:        ptrStr(name),
		MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
		Disks: []*computepb.AttachedDisk{{
			Boot:       ptrBool(true),
			AutoDelete: ptrBool(true),
			InitializeParams: &computepb.AttachedDiskInitializeParams{
				SourceImage: ptrStr("projects/debian-cloud/global/images/family/debian-12"),
				DiskSizeGb:  ptrInt64(20),
			},
		}},
	})

	got := mustGet(t, instances, testZone, name)
	if got.GetDisks()[0].GetDiskSizeGb() != 20 {
		t.Fatalf("initial diskSizeGb=%d want 20", got.GetDisks()[0].GetDiskSizeGb())
	}

	resizeOp, err := disks.Resize(ctx, &computepb.ResizeDiskRequest{
		Project: testProject, Zone: testZone, Disk: name,
		DisksResizeRequestResource: &computepb.DisksResizeRequest{SizeGb: ptrInt64(50)},
	})
	if err != nil {
		t.Fatalf("Resize: %v", err)
	}

	if err := resizeOp.Wait(ctx); err != nil {
		t.Fatalf("Resize wait: %v", err)
	}

	got2 := mustGet(t, instances, testZone, name)
	if size := got2.GetDisks()[0].GetDiskSizeGb(); size != 50 {
		t.Errorf("diskSizeGb after resize=%d want 50 (instance view is stale)", size)
	}
}

// TestSDKAddDeleteAccessConfig guards a real-user e2e finding: instances.
// addAccessConfig/deleteAccessConfig (used by "gcloud compute instances
// add-access-config"/"delete-access-config" to attach/detach an ephemeral
// external IP post-creation) returned 501 not implemented.
func TestSDKAddDeleteAccessConfig(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Compute: cloudP.GCE, Networking: cloudP.VPC})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	instances := newSDKInstancesClient(t, ts)
	ctx := context.Background()

	name := "accessconfig-vm"
	mustInsert(t, instances, testZone, &computepb.Instance{
		Name:        ptrStr(name),
		MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
		NetworkInterfaces: []*computepb.NetworkInterface{
			{Network: ptrStr("global/networks/default")},
		},
	})

	got := mustGet(t, instances, testZone, name)
	if acs := got.GetNetworkInterfaces()[0].GetAccessConfigs(); len(acs) != 0 {
		t.Fatalf("fresh instance has %d accessConfigs, want 0", len(acs))
	}

	addOp, err := instances.AddAccessConfig(ctx, &computepb.AddAccessConfigInstanceRequest{
		Project: testProject, Zone: testZone, Instance: name, NetworkInterface: "nic0",
		AccessConfigResource: &computepb.AccessConfig{},
	})
	if err != nil {
		t.Fatalf("AddAccessConfig: %v", err)
	}

	if err := addOp.Wait(ctx); err != nil {
		t.Fatalf("AddAccessConfig wait: %v", err)
	}

	got2 := mustGet(t, instances, testZone, name)
	acs := got2.GetNetworkInterfaces()[0].GetAccessConfigs()

	if len(acs) != 1 {
		t.Fatalf("accessConfigs after add=%d want 1", len(acs))
	}

	if acs[0].GetNatIP() == "" {
		t.Error("added accessConfig has no synthesized natIP")
	}

	if acs[0].GetType() != "ONE_TO_ONE_NAT" {
		t.Errorf("accessConfig type=%q want ONE_TO_ONE_NAT", acs[0].GetType())
	}

	name0 := acs[0].GetName()

	delOp, err := instances.DeleteAccessConfig(ctx, &computepb.DeleteAccessConfigInstanceRequest{
		Project: testProject, Zone: testZone, Instance: name,
		AccessConfig: name0, NetworkInterface: "nic0",
	})
	if err != nil {
		t.Fatalf("DeleteAccessConfig: %v", err)
	}

	if err := delOp.Wait(ctx); err != nil {
		t.Fatalf("DeleteAccessConfig wait: %v", err)
	}

	got3 := mustGet(t, instances, testZone, name)
	if acs := got3.GetNetworkInterfaces()[0].GetAccessConfigs(); len(acs) != 0 {
		t.Errorf("accessConfigs after delete=%d want 0", len(acs))
	}
}

// TestSDKDeletionProtection guards a real-user e2e finding: deletionProtection
// was silently dropped on insert (always read back false) and never enforced,
// so instances.delete succeeded on a protected instance. It should round-trip,
// block delete with a 400 while true, and instances.setDeletionProtection
// should be able to clear it so delete then succeeds.
func TestSDKDeletionProtection(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Compute: cloudP.GCE})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	instances := newSDKInstancesClient(t, ts)
	ctx := context.Background()

	name := "delprotect-vm"
	mustInsert(t, instances, testZone, &computepb.Instance{
		Name:               ptrStr(name),
		MachineType:        ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
		DeletionProtection: ptrBool(true),
	})

	got := mustGet(t, instances, testZone, name)
	if !got.GetDeletionProtection() {
		t.Fatal("deletionProtection not round-tripped from insert")
	}

	if _, err := instances.Delete(ctx, &computepb.DeleteInstanceRequest{
		Project: testProject, Zone: testZone, Instance: name,
	}); err == nil {
		t.Error("Delete of a deletionProtection=true instance succeeded, want an error")
	}

	// Instance must still exist.
	mustGet(t, instances, testZone, name)

	clearOp, err := instances.SetDeletionProtection(ctx, &computepb.SetDeletionProtectionInstanceRequest{
		Project: testProject, Zone: testZone, Resource: name, DeletionProtection: ptrBool(false),
	})
	if err != nil {
		t.Fatalf("SetDeletionProtection: %v", err)
	}

	if err := clearOp.Wait(ctx); err != nil {
		t.Fatalf("SetDeletionProtection wait: %v", err)
	}

	got2 := mustGet(t, instances, testZone, name)
	if got2.GetDeletionProtection() {
		t.Error("deletionProtection still true after setDeletionProtection(false)")
	}

	delOp, err := instances.Delete(ctx, &computepb.DeleteInstanceRequest{
		Project: testProject, Zone: testZone, Instance: name,
	})
	if err != nil {
		t.Fatalf("Delete after clearing protection: %v", err)
	}

	if err := delOp.Wait(ctx); err != nil {
		t.Fatalf("Delete wait: %v", err)
	}
}
