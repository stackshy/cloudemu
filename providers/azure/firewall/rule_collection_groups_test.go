package firewall

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/azurefirewall/driver"
)

func TestRuleCollectionGroupsSurviveSnapshotAndGoWithPolicy(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	if _, _, err := src.CreateOrUpdateRuleCollectionGroup(ctx, "rg", "fp", "g", driver.RuleCollectionGroup{Priority: 500}); err == nil {
		t.Fatal("group created under a missing policy")
	}

	if _, _, err := src.CreateOrUpdateFirewallPolicy(ctx, "rg", "fp", driver.FirewallPolicy{}); err != nil {
		t.Fatal(err)
	}

	g := driver.RuleCollectionGroup{Priority: 500, RuleCollections: []any{
		map[string]any{"name": "c1", "priority": float64(200)},
	}}
	if _, _, err := src.CreateOrUpdateRuleCollectionGroup(ctx, "rg", "fp", "g", g); err != nil {
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

	got, err := dst.GetRuleCollectionGroup(ctx, "RG", "FP", "G")
	if err != nil || got.Priority != 500 || len(got.RuleCollections) != 1 {
		t.Fatalf("group after restore = %v, %v", got, err)
	}

	if err := dst.DeleteFirewallPolicy(ctx, "rg", "fp"); err != nil {
		t.Fatal(err)
	}

	if _, err := dst.GetRuleCollectionGroup(ctx, "rg", "fp", "g"); err == nil {
		t.Error("group outlived its policy")
	}
}
