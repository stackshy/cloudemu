package compute_test

import (
	"context"
	"strings"
	"testing"

	"cloud.google.com/go/compute/apiv1/computepb"
)

// TestSDKDeleteAttachedDiskRejected proves deleting a disk that is still
// attached to an instance is rejected with 400 resourceInUseByAnotherResource
// (real GCP behavior), and that the disk deletes cleanly once detached.
func TestSDKDeleteAttachedDiskRejected(t *testing.T) {
	ts := newGCPServer(t)
	disks := newDisksSDKClient(t, ts)
	instances := newSDKInstancesClient(t, ts)
	ctx := context.Background()

	insertDisk(t, disks, &computepb.Disk{Name: ptrStr("attached-disk"), SizeGb: ptrInt64(10)})

	insOp, err := instances.Insert(ctx, &computepb.InsertInstanceRequest{
		Project: testProject, Zone: testZone,
		InstanceResource: &computepb.Instance{
			Name:        ptrStr("disk-holder"),
			MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
			NetworkInterfaces: []*computepb.NetworkInterface{
				{Network: ptrStr("global/networks/default")},
			},
		},
	})
	if err != nil {
		t.Fatalf("instance Insert: %v", err)
	}

	if err := insOp.Wait(ctx); err != nil {
		t.Fatalf("instance Insert wait: %v", err)
	}

	diskURL := "projects/" + testProject + "/zones/" + testZone + "/disks/attached-disk"

	attachOp, err := instances.AttachDisk(ctx, &computepb.AttachDiskInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "disk-holder",
		AttachedDiskResource: &computepb.AttachedDisk{
			Source: ptrStr(diskURL), DeviceName: ptrStr("data"),
		},
	})
	if err != nil {
		t.Fatalf("AttachDisk: %v", err)
	}

	if err := attachOp.Wait(ctx); err != nil {
		t.Fatalf("AttachDisk wait: %v", err)
	}

	// Delete while attached must fail with resourceInUseByAnotherResource (400).
	_, err = disks.Delete(ctx, &computepb.DeleteDiskRequest{
		Project: testProject, Zone: testZone, Disk: "attached-disk",
	})
	if err == nil {
		t.Fatal("Delete of attached disk succeeded, want 400 resourceInUseByAnotherResource")
	}

	if !strings.Contains(err.Error(), "resourceInUseByAnotherResource") && !strings.Contains(err.Error(), "400") {
		t.Errorf("Delete error = %v, want resourceInUseByAnotherResource/400", err)
	}

	// The disk survived the rejected delete.
	if _, err := disks.Get(ctx, &computepb.GetDiskRequest{
		Project: testProject, Zone: testZone, Disk: "attached-disk",
	}); err != nil {
		t.Fatalf("Get after rejected delete: %v (disk should still exist)", err)
	}

	// Detach, then delete must succeed.
	detachOp, err := instances.DetachDisk(ctx, &computepb.DetachDiskInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "disk-holder", DeviceName: "data",
	})
	if err != nil {
		t.Fatalf("DetachDisk: %v", err)
	}

	if err := detachOp.Wait(ctx); err != nil {
		t.Fatalf("DetachDisk wait: %v", err)
	}

	delOp, err := disks.Delete(ctx, &computepb.DeleteDiskRequest{
		Project: testProject, Zone: testZone, Disk: "attached-disk",
	})
	if err != nil {
		t.Fatalf("Delete after detach: %v (should succeed)", err)
	}

	if err := delOp.Wait(ctx); err != nil {
		t.Fatalf("Delete after detach wait: %v", err)
	}
}

// TestSDKDeleteUnattachedDiskSucceeds proves the in-use guard does not block a
// plain, never-attached disk from being deleted.
func TestSDKDeleteUnattachedDiskSucceeds(t *testing.T) {
	ts := newGCPServer(t)
	disks := newDisksSDKClient(t, ts)
	ctx := context.Background()

	insertDisk(t, disks, &computepb.Disk{Name: ptrStr("free-disk"), SizeGb: ptrInt64(10)})

	delOp, err := disks.Delete(ctx, &computepb.DeleteDiskRequest{
		Project: testProject, Zone: testZone, Disk: "free-disk",
	})
	if err != nil {
		t.Fatalf("Delete of unattached disk: %v", err)
	}

	if err := delOp.Wait(ctx); err != nil {
		t.Fatalf("Delete wait: %v", err)
	}
}
