package cosmospostgresql

import (
	"context"
	"testing"

	cpgdriver "github.com/stackshy/cloudemu/v2/services/cosmospostgresql/driver"
)

// TestSnapshotRoundTripCosmosPostgreSQL proves a snapshot/restore round-trip
// preserves a cluster and a child firewall rule under their composite ARM keys.
func TestSnapshotRoundTripCosmosPostgreSQL(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	if _, _, err := src.CreateOrUpdateCluster(ctx, cpgdriver.CreateClusterConfig{
		Name: "pg1", ResourceGroup: "rg1", Location: "eastus", NodeCount: 2,
		Tags: map[string]string{"env": "prod"},
	}); err != nil {
		t.Fatalf("CreateOrUpdateCluster: %v", err)
	}

	if _, err := src.CreateOrUpdateFirewallRule(ctx, cpgdriver.CreateFirewallRuleConfig{
		ResourceGroup: "rg1", ClusterName: "pg1", Name: "allow-all",
		StartIPAddress: "0.0.0.0", EndIPAddress: "255.255.255.255",
	}); err != nil {
		t.Fatalf("CreateOrUpdateFirewallRule: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	c, err := dst.GetCluster(ctx, "rg1", "pg1")
	if err != nil || c.Name != "pg1" || c.Tags["env"] != "prod" || c.NodeCount != 2 {
		t.Fatalf("restored cluster = %+v, err %v", c, err)
	}

	fr, err := dst.GetFirewallRule(ctx, "rg1", "pg1", "allow-all")
	if err != nil || fr.StartIPAddress != "0.0.0.0" {
		t.Fatalf("restored firewall rule = %+v, err %v", fr, err)
	}
}
