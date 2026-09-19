package compute_test

import (
	"slices"
	"testing"

	"cloud.google.com/go/compute/apiv1/computepb"
)

// TestGCEZoneIsolation verifies GCP's location isolation (path-scoped filtering
// in the wire handler, not a code change from the AWS multi-region work): an
// instance created in one zone is not listed or gettable from another zone, and
// is present in its own. This is the GCP counterpart to the AWS region-isolation
// guarantee.
func TestGCEZoneIsolation(t *testing.T) {
	client, _, ctx := newInstancesEnv(t)

	mustInsert(t, client, testZone, &computepb.Instance{
		Name:        ptrStr("zoned-vm"),
		MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
	})

	// Absent in another zone.
	other := listNames(t, client.List(ctx, &computepb.ListInstancesRequest{Project: testProject, Zone: altZone}))
	if slices.Contains(other, "zoned-vm") {
		t.Fatalf("instance visible in zone %s: %v (cross-zone leak)", altZone, other)
	}

	if _, err := client.Get(ctx, &computepb.GetInstanceRequest{
		Project: testProject, Zone: altZone, Instance: "zoned-vm",
	}); err == nil {
		t.Fatalf("Get in zone %s succeeded, want not-found (isolation)", altZone)
	}

	// Present in its own zone.
	own := listNames(t, client.List(ctx, &computepb.ListInstancesRequest{Project: testProject, Zone: testZone}))
	if !slices.Contains(own, "zoned-vm") {
		t.Fatalf("instance not listed in its own zone %s: %v", testZone, own)
	}
}
