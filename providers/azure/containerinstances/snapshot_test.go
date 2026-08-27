package containerinstances

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/containerinstances/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// TestSnapshotRoundTripContainerInstances proves a snapshot/restore round-trip
// preserves a container group under its composite ARM identity.
func TestSnapshotRoundTripContainerInstances(t *testing.T) {
	ctx := context.Background()
	src := New(config.NewOptions())

	sc := scope.Scope{Subscription: "sub1", ResourceGroup: "rg1"}
	if _, err := src.CreateContainerGroup(ctx, driver.ContainerGroupConfig{
		Name:     "cg1",
		Location: "eastus",
		OSType:   "Linux",
		Containers: []driver.ContainerConfig{
			{Name: "c1", Image: "nginx:latest"},
		},
		Tags:  map[string]string{"env": "prod"},
		Scope: sc,
	}); err != nil {
		t.Fatalf("CreateContainerGroup: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := New(config.NewOptions())
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	got, err := dst.GetContainerGroup(ctx, "sub1", "rg1", "cg1")
	if err != nil {
		t.Fatalf("GetContainerGroup: %v", err)
	}

	if got.Name != "cg1" || got.Tags["env"] != "prod" || len(got.Containers) != 1 {
		t.Fatalf("restored group = %+v", got)
	}
}
