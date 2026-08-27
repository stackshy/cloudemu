package managedcassandra

import (
	"context"
	"testing"

	mcdriver "github.com/stackshy/cloudemu/v2/services/managedcassandra/driver"
)

// TestSnapshotRoundTripManagedCassandra proves a snapshot/restore round-trip
// preserves a cluster and a child data center under their composite ARM keys.
func TestSnapshotRoundTripManagedCassandra(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	if _, err := src.CreateOrUpdateCluster(ctx, mcdriver.CreateClusterConfig{
		Name: "cass", ResourceGroup: "rg1", Location: "eastus",
		DelegatedManagementSubnetID: "/subnets/sn", Tags: map[string]string{"env": "prod"},
	}); err != nil {
		t.Fatalf("CreateOrUpdateCluster: %v", err)
	}

	if _, err := src.CreateOrUpdateDataCenter(ctx, mcdriver.CreateDataCenterConfig{
		ClusterName: "cass", ResourceGroup: "rg1", Name: "dc1",
		DataCenterLocation: "eastus", NodeCount: 3, DelegatedSubnetID: "/subnets/sn",
	}); err != nil {
		t.Fatalf("CreateOrUpdateDataCenter: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	c, err := dst.GetCluster(ctx, "rg1", "cass")
	if err != nil || c.Name != "cass" || c.Tags["env"] != "prod" {
		t.Fatalf("restored cluster = %+v, err %v", c, err)
	}

	dc, err := dst.GetDataCenter(ctx, "rg1", "cass", "dc1")
	if err != nil || dc.NodeCount != 3 {
		t.Fatalf("restored data center = %+v, err %v", dc, err)
	}
}
