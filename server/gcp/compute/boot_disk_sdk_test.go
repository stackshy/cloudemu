package compute_test

import (
	"strconv"
	"strings"
	"sync"
	"testing"

	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/iterator"
)

// TestSDKInsertMaterializesBootDisk proves instances.insert creates the boot
// disk as a real Disk resource: disks.get returns it, attached to the instance,
// with the size / type / sourceImage the initializeParams asked for.
func TestSDKInsertMaterializesBootDisk(t *testing.T) {
	client, ts, ctx := newInstancesEnv(t)
	disks := newDisksSDKClient(t, ts)

	mustInsert(t, client, testZone, &computepb.Instance{
		Name:        ptrStr("boot-vm"),
		MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
		Disks: []*computepb.AttachedDisk{{
			Boot:       ptrBool(true),
			AutoDelete: ptrBool(true),
			InitializeParams: &computepb.AttachedDiskInitializeParams{
				SourceImage: ptrStr("projects/debian-cloud/global/images/family/debian-12"),
				DiskSizeGb:  ptrInt64(20),
				DiskType:    ptrStr("zones/" + testZone + "/diskTypes/pd-ssd"),
			},
		}},
	})

	// The boot disk defaults to the instance's name in real GCP.
	got, err := disks.Get(ctx, &computepb.GetDiskRequest{
		Project: testProject, Zone: testZone, Disk: "boot-vm",
	})
	if err != nil {
		t.Fatalf("disks.Get(boot-vm): %v", err)
	}

	if got.GetSizeGb() != 20 {
		t.Errorf("sizeGb=%d want 20", got.GetSizeGb())
	}

	if !strings.HasSuffix(got.GetType(), "/diskTypes/pd-ssd") {
		t.Errorf("type=%q want .../diskTypes/pd-ssd", got.GetType())
	}

	if !strings.HasSuffix(got.GetSourceImage(), "/family/debian-12") {
		t.Errorf("sourceImage=%q want .../family/debian-12", got.GetSourceImage())
	}

	users := got.GetUsers()
	if len(users) != 1 || !strings.HasSuffix(users[0], "/instances/boot-vm") {
		t.Errorf("users=%v want one link ending /instances/boot-vm", users)
	}
}

// TestSDKInsertMaterializesAdditionalDisk proves a second initializeParams disk
// in the insert body is materialized alongside the boot disk.
func TestSDKInsertMaterializesAdditionalDisk(t *testing.T) {
	client, ts, ctx := newInstancesEnv(t)
	disks := newDisksSDKClient(t, ts)

	mustInsert(t, client, testZone, &computepb.Instance{
		Name:        ptrStr("multi-vm"),
		MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
		Disks: []*computepb.AttachedDisk{
			{
				Boot:       ptrBool(true),
				AutoDelete: ptrBool(true),
				InitializeParams: &computepb.AttachedDiskInitializeParams{
					SourceImage: ptrStr("projects/debian-cloud/global/images/family/debian-12"),
				},
			},
			{
				AutoDelete: ptrBool(true),
				DeviceName: ptrStr("data"),
				InitializeParams: &computepb.AttachedDiskInitializeParams{
					DiskName:   ptrStr("multi-vm-data"),
					DiskSizeGb: ptrInt64(50),
				},
			},
		},
	})

	for _, name := range []string{"multi-vm", "multi-vm-data"} {
		if _, err := disks.Get(ctx, &computepb.GetDiskRequest{
			Project: testProject, Zone: testZone, Disk: name,
		}); err != nil {
			t.Errorf("disks.Get(%s): %v — disk should have materialized", name, err)
		}
	}
}

// TestSDKDeleteHonorsAutoDelete proves instances.delete deletes an autoDelete=true
// disk (the boot disk) and detaches — but keeps — an autoDelete=false disk.
func TestSDKDeleteHonorsAutoDelete(t *testing.T) {
	client, ts, ctx := newInstancesEnv(t)
	disks := newDisksSDKClient(t, ts)

	insertDisk(t, disks, &computepb.Disk{Name: ptrStr("keep-disk"), SizeGb: ptrInt64(10)})

	mustInsert(t, client, testZone, &computepb.Instance{
		Name:        ptrStr("cascade-vm"),
		MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
		Disks: []*computepb.AttachedDisk{{
			Boot:       ptrBool(true),
			AutoDelete: ptrBool(true),
			InitializeParams: &computepb.AttachedDiskInitializeParams{
				SourceImage: ptrStr("projects/debian-cloud/global/images/family/debian-12"),
			},
		}},
	})

	// Attach keep-disk with autoDelete=false.
	attachOp, err := client.AttachDisk(ctx, &computepb.AttachDiskInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "cascade-vm",
		AttachedDiskResource: &computepb.AttachedDisk{
			DeviceName: ptrStr("keep"),
			AutoDelete: ptrBool(false),
			Source:     ptrStr("zones/" + testZone + "/disks/keep-disk"),
		},
	})
	if err != nil {
		t.Fatalf("AttachDisk: %v", err)
	}

	if err := attachOp.Wait(ctx); err != nil {
		t.Fatalf("AttachDisk wait: %v", err)
	}

	delOp, err := client.Delete(ctx, &computepb.DeleteInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "cascade-vm",
	})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if err := delOp.Wait(ctx); err != nil {
		t.Fatalf("Delete wait: %v", err)
	}

	// Boot disk (autoDelete=true) is gone.
	if _, err := disks.Get(ctx, &computepb.GetDiskRequest{
		Project: testProject, Zone: testZone, Disk: "cascade-vm",
	}); err == nil {
		t.Error("boot disk survived delete, want autoDelete cascade to remove it")
	}

	// keep-disk (autoDelete=false) survives, detached (no users).
	kept, err := disks.Get(ctx, &computepb.GetDiskRequest{
		Project: testProject, Zone: testZone, Disk: "keep-disk",
	})
	if err != nil {
		t.Fatalf("keep-disk should survive: %v", err)
	}

	if users := kept.GetUsers(); len(users) != 0 {
		t.Errorf("keep-disk users=%v want empty (detached on delete)", users)
	}
}

// TestSDKAttachDetachFlipsDriverVolume proves attach/detach round-trip through
// the unified disk model: after attach the disk reports the instance in users[];
// after detach it is released and can be deleted.
func TestSDKAttachDetachFlipsDriverVolume(t *testing.T) {
	client, ts, ctx := newInstancesEnv(t)
	disks := newDisksSDKClient(t, ts)

	insertDisk(t, disks, &computepb.Disk{Name: ptrStr("roundtrip-disk"), SizeGb: ptrInt64(10)})

	mustInsert(t, client, testZone, &computepb.Instance{
		Name:        ptrStr("rt-vm"),
		MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
	})

	attachOp, err := client.AttachDisk(ctx, &computepb.AttachDiskInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "rt-vm",
		AttachedDiskResource: &computepb.AttachedDisk{
			DeviceName: ptrStr("data"),
			Source:     ptrStr("zones/" + testZone + "/disks/roundtrip-disk"),
		},
	})
	if err != nil {
		t.Fatalf("AttachDisk: %v", err)
	}

	if err := attachOp.Wait(ctx); err != nil {
		t.Fatalf("AttachDisk wait: %v", err)
	}

	attached, err := disks.Get(ctx, &computepb.GetDiskRequest{
		Project: testProject, Zone: testZone, Disk: "roundtrip-disk",
	})
	if err != nil {
		t.Fatalf("Get after attach: %v", err)
	}

	if users := attached.GetUsers(); len(users) != 1 || !strings.HasSuffix(users[0], "/instances/rt-vm") {
		t.Errorf("after attach users=%v want [.../instances/rt-vm]", users)
	}

	detachOp, err := client.DetachDisk(ctx, &computepb.DetachDiskInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "rt-vm", DeviceName: "data",
	})
	if err != nil {
		t.Fatalf("DetachDisk: %v", err)
	}

	if err := detachOp.Wait(ctx); err != nil {
		t.Fatalf("DetachDisk wait: %v", err)
	}

	released, err := disks.Get(ctx, &computepb.GetDiskRequest{
		Project: testProject, Zone: testZone, Disk: "roundtrip-disk",
	})
	if err != nil {
		t.Fatalf("Get after detach: %v", err)
	}

	if users := released.GetUsers(); len(users) != 0 {
		t.Errorf("after detach users=%v want empty", users)
	}

	// A released disk can be deleted (the in-use guard no longer blocks it).
	delOp, err := disks.Delete(ctx, &computepb.DeleteDiskRequest{
		Project: testProject, Zone: testZone, Disk: "roundtrip-disk",
	})
	if err != nil {
		t.Fatalf("Delete after detach: %v", err)
	}

	if err := delOp.Wait(ctx); err != nil {
		t.Fatalf("Delete after detach wait: %v", err)
	}
}

// TestSDKInsertDeleteRaceWithDiskList hammers instances.insert + delete (each
// materializing then cascading a boot disk) concurrently with disks.list, so the
// suite catches any data race between the copy-on-write disk mutations and the
// ranging list read under `go test -race`.
func TestSDKInsertDeleteRaceWithDiskList(t *testing.T) {
	client, ts, ctx := newInstancesEnv(t)
	disks := newDisksSDKClient(t, ts)

	const (
		workers = 6
		iters   = 12
	)

	var wg sync.WaitGroup

	for w := range workers {
		wg.Add(1)

		go func(w int) {
			defer wg.Done()

			for i := range iters {
				name := ptrStr("race-vm-" + strconv.Itoa(w) + "-" + strconv.Itoa(i))

				insOp, err := client.Insert(ctx, &computepb.InsertInstanceRequest{
					Project: testProject, Zone: testZone,
					InstanceResource: &computepb.Instance{
						Name:        name,
						MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
						Disks: []*computepb.AttachedDisk{{
							Boot:       ptrBool(true),
							AutoDelete: ptrBool(true),
							InitializeParams: &computepb.AttachedDiskInitializeParams{
								SourceImage: ptrStr("projects/debian-cloud/global/images/family/debian-12"),
							},
						}},
					},
				})
				if err != nil {
					t.Errorf("Insert: %v", err)
					return
				}

				_ = insOp.Wait(ctx)

				delOp, err := client.Delete(ctx, &computepb.DeleteInstanceRequest{
					Project: testProject, Zone: testZone, Instance: *name,
				})
				if err != nil {
					t.Errorf("Delete: %v", err)
					return
				}

				_ = delOp.Wait(ctx)
			}
		}(w)
	}

	// Concurrent readers ranging the disk store.
	for range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for range iters * 2 {
				it := disks.List(ctx, &computepb.ListDisksRequest{Project: testProject, Zone: testZone})
				for {
					if _, err := it.Next(); err != nil {
						break
					}
				}
			}
		}()
	}

	wg.Wait()

	// Every autoDelete boot disk was cascaded on its instance's delete, so the
	// disk store settles empty.
	it := disks.List(ctx, &computepb.ListDisksRequest{Project: testProject, Zone: testZone})

	count := 0

	for {
		_, err := it.Next()
		if err == iterator.Done {
			break
		}

		if err != nil {
			t.Fatalf("final List: %v", err)
		}

		count++
	}

	if count != 0 {
		t.Errorf("disks left after all deletes = %d, want 0 (autoDelete cascade leaked)", count)
	}
}
