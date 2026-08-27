package mysqlflex

import (
	"context"
	"testing"

	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

func TestSnapshotRoundTripMySQLFlex(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	if _, err := src.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "srv"}); err != nil {
		t.Fatalf("create instance: %v", err)
	}

	if _, err := src.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "srv", Name: "app"}); err != nil {
		t.Fatalf("create database: %v", err)
	}

	if _, err := src.CreateFirewallRule(ctx, rdsdriver.FirewallRuleConfig{
		Server: "srv", Name: "r", StartIPAddress: "10.0.0.1", EndIPAddress: "10.0.0.9",
	}); err != nil {
		t.Fatalf("create firewall rule: %v", err)
	}

	data, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatalf("restore: %v", err)
	}

	instances, err := dst.DescribeInstances(ctx, nil)
	if err != nil {
		t.Fatalf("describe instances: %v", err)
	}
	if len(instances) != 1 || instances[0].ID != "srv" {
		t.Fatalf("instances = %+v, want one with ID srv", instances)
	}

	dbs, err := dst.ListDatabases(ctx, "srv")
	if err != nil {
		t.Fatalf("list databases: %v", err)
	}
	if len(dbs) != 1 {
		t.Fatalf("databases = %d, want 1", len(dbs))
	}

	rules, err := dst.ListFirewallRules(ctx, "srv")
	if err != nil {
		t.Fatalf("list firewall rules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("firewall rules = %d, want 1", len(rules))
	}
}
