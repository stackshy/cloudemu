package virtualmachines

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

func TestExtensionsAndAvailabilitySetsSurviveSnapshot(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	insts, err := src.RunInstances(ctx, driver.InstanceConfig{ImageID: "img", InstanceType: "Standard_B1s"}, 1)
	if err != nil {
		t.Fatal(err)
	}

	id := insts[0].ID
	ext := driver.AzureVMExtension{Name: "cse", Properties: map[string]any{
		"settings": map[string]any{"a": "b"}, "protectedSettings": map[string]any{"secret": "x"},
	}}

	if _, _, err := src.PutVMExtension(ctx, id, ext); err != nil {
		t.Fatal(err)
	}

	set := driver.AzureAvailabilitySet{
		Name: "as", Subscription: "s1", ResourceGroup: "rg1", PlatformFaultDomainCount: 2, PlatformUpdateDomainCount: 5,
	}
	if _, err := src.PutAvailabilitySet(ctx, set); err != nil {
		t.Fatal(err)
	}

	data, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatal(err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatal(err)
	}

	got, err := dst.GetVMExtension(ctx, id, "CSE")
	if err != nil {
		t.Fatalf("extension after restore: %v", err)
	}

	if _, leaked := got.Properties["protectedSettings"]; leaked {
		t.Error("protectedSettings was stored")
	}

	if _, err := dst.GetAvailabilitySet(ctx, "S1", "RG1", "AS"); err != nil {
		t.Errorf("availability set after restore: %v", err)
	}

	if err := dst.DeleteInstances(ctx, []string{id}); err != nil {
		t.Fatal(err)
	}

	if _, err := dst.GetVMExtension(ctx, id, "cse"); err == nil {
		t.Error("extension outlived its VM")
	}

	if err := dst.PurgeComputeResourceGroup(ctx, "s1", "rg1"); err != nil {
		t.Fatal(err)
	}

	if _, err := dst.GetAvailabilitySet(ctx, "s1", "rg1", "as"); err == nil {
		t.Error("availability set survived its group's purge")
	}
}
