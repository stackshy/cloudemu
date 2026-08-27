package networkfirewall

import (
	"context"
	"testing"

	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

// TestSnapshotRoundTripNetworkFirewall proves a snapshot/restore round-trip
// preserves the firewall/policy/rule-group stores and the mu-guarded logging map
// under their original identities.
func TestSnapshotRoundTripNetworkFirewall(t *testing.T) {
	ctx := context.Background()
	src := newMock()

	pol, err := src.CreateFirewallPolicy(ctx, nfdriver.CreateFirewallPolicyConfig{
		Name: "pol-1", Description: "policy one",
	})
	if err != nil {
		t.Fatalf("create policy: %v", err)
	}

	fw, err := src.CreateFirewall(ctx, nfdriver.CreateFirewallConfig{
		Name: "fw-1", VPCID: "vpc-1", SubnetIDs: []string{"subnet-1"}, PolicyARN: pol.ARN,
	})
	if err != nil {
		t.Fatalf("create firewall: %v", err)
	}

	if _, err := src.CreateRuleGroup(ctx, nfdriver.CreateRuleGroupConfig{
		Name: "rg-1", Type: "STATEFUL", Capacity: 100,
	}); err != nil {
		t.Fatalf("create rule group: %v", err)
	}

	if err := src.UpdateLoggingConfiguration(ctx, "fw-1", []string{"FLOW", "ALERT"}); err != nil {
		t.Fatalf("update logging: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	gotFw, err := dst.DescribeFirewall(ctx, "fw-1", "")
	if err != nil || gotFw.ARN != fw.ARN || gotFw.PolicyARN != pol.ARN {
		t.Fatalf("restored firewall = %+v, err %v", gotFw, err)
	}

	gotPol, err := dst.DescribeFirewallPolicy(ctx, "pol-1", "")
	if err != nil || gotPol.Description != "policy one" {
		t.Fatalf("restored policy = %+v, err %v", gotPol, err)
	}

	// Rule group keyed by "TYPE/name" resolves through the type-qualified lookup.
	gotRg, err := dst.DescribeRuleGroup(ctx, "rg-1", "", "STATEFUL")
	if err != nil || gotRg.Capacity != 100 {
		t.Fatalf("restored rule group = %+v, err %v", gotRg, err)
	}

	// Logging (mu-guarded) survived.
	logs, err := dst.DescribeLoggingConfiguration(ctx, "fw-1")
	if err != nil || len(logs) != 2 || logs[0] != "FLOW" || logs[1] != "ALERT" {
		t.Fatalf("restored logging = %+v, err %v", logs, err)
	}
}
