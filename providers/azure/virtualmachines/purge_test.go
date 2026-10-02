package virtualmachines

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/compute"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

func mustRun(t *testing.T, m *Mock, rg string) string {
	t.Helper()

	insts, err := m.RunInstances(context.Background(), driver.InstanceConfig{
		ImageID: "img", InstanceType: "Standard_B1s", ResourceGroup: rg,
	}, 1)
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	return insts[0].ID
}

func TestDeleteInstancesRemovesVM(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	id := mustRun(t, m, "rg1")

	if err := m.DeleteInstances(ctx, []string{id}); err != nil {
		t.Fatalf("DeleteInstances: %v", err)
	}

	if _, ok := m.instances.Get(id); ok {
		t.Fatal("VM still stored after DeleteInstances")
	}

	if err := m.DeleteInstances(ctx, []string{id}); err == nil {
		t.Fatal("second DeleteInstances: want NotFound, got nil")
	}
}

func TestPurgeComputeResourceGroup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	tags := func(rg string) map[string]string { return map[string]string{rgTag: rg} }

	gone := mustRun(t, m, "Cas1")
	kept := mustRun(t, m, "cas10")
	ghost := mustRun(t, m, "cas1")

	if err := m.TerminateInstances(ctx, []string{ghost}); err != nil {
		t.Fatal(err)
	}

	for _, rg := range []string{"Cas1", "cas10"} {
		if _, err := m.CreateScaleSet(ctx, ScaleSet{Name: "ss-" + rg, ResourceGroup: rg}); err != nil {
			t.Fatal(err)
		}

		vol, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 4, Tags: tags(rg)})
		if err != nil {
			t.Fatal(err)
		}

		if _, err := m.CreateSnapshot(ctx, driver.SnapshotConfig{VolumeID: vol.ID, Tags: tags(rg)}); err != nil {
			t.Fatal(err)
		}

		if _, err := m.CreateImage(ctx, driver.ImageConfig{Name: "i-" + rg, OSDiskID: "d", Tags: tags(rg)}); err != nil {
			t.Fatal(err)
		}

		if _, err := m.CreateKeyPair(ctx, driver.KeyPairConfig{Name: "k-" + rg, Tags: tags(rg)}); err != nil {
			t.Fatal(err)
		}
	}

	if err := m.PurgeComputeResourceGroup(ctx, "cas1"); err != nil {
		t.Fatalf("PurgeComputeResourceGroup: %v", err)
	}

	for _, id := range []string{gone, ghost} {
		if _, ok := m.instances.Get(id); ok {
			t.Errorf("VM %s survived its group's purge", id)
		}
	}

	if _, ok := m.instances.Get(kept); !ok {
		t.Error("VM in cas10 was purged with cas1")
	}

	counts := map[string]int{
		"scale sets": len(m.scaleSets.All()), "disks": len(m.volumes.All()),
		"snapshots": len(m.snapshots.All()), "images": len(m.images.All()), "keys": len(m.keyPairs.All()),
	}

	for kind, n := range counts {
		if n != 1 {
			t.Errorf("%s left after purge = %d, want 1 (the cas10 one)", kind, n)
		}
	}
}

func TestRestoreDropsTerminatedVMs(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()
	live := mustRun(t, src, "rg1")
	dead := mustRun(t, src, "rg1")

	if err := src.TerminateInstances(ctx, []string{dead}); err != nil {
		t.Fatal(err)
	}

	data, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatal(err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatal(err)
	}

	if _, ok := dst.instances.Get(dead); ok {
		t.Error("terminated VM was restored")
	}

	if inst, ok := dst.instances.Get(live); !ok || inst.State == compute.StateTerminated {
		t.Error("running VM was not restored")
	}
}
