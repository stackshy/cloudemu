package sql

import (
	"context"
	"testing"

	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

func TestSnapshotRoundTripSQL(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	if _, err := src.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "srv1"}); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	if _, err := src.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db1", ClusterID: "srv1"}); err != nil {
		t.Fatalf("create instance: %v", err)
	}

	if _, err := src.CreateFirewallRule(ctx, rdsdriver.FirewallRuleConfig{
		Server: "srv1", Name: "rule1", StartIPAddress: "10.0.0.1", EndIPAddress: "10.0.0.9",
	}); err != nil {
		t.Fatalf("create firewall rule: %v", err)
	}

	data, err := src.Snapshot(ctx, true)
	requireNoError(t, err)

	dst := newTestMock()
	requireNoError(t, dst.Restore(ctx, data))

	clusters, err := dst.DescribeClusters(ctx, nil)
	requireNoError(t, err)
	assertEqual(t, 1, len(clusters))
	assertEqual(t, "srv1", clusters[0].ID)

	instances, err := dst.DescribeInstances(ctx, nil)
	requireNoError(t, err)
	assertEqual(t, 1, len(instances))

	rules, err := dst.ListFirewallRules(ctx, "srv1")
	requireNoError(t, err)
	assertEqual(t, 1, len(rules))
}
