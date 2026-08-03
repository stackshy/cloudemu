package networkfirewall

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

func newMock() *Mock { return New(config.NewOptions()) }

func TestFirewallLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, err := m.CreateFirewall(ctx, nfdriver.CreateFirewallConfig{}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("firewall without name: got %v, want InvalidArgument", err)
	}

	fw, err := m.CreateFirewall(ctx, nfdriver.CreateFirewallConfig{
		Name: "fw-1", VPCID: "vpc-1", SubnetIDs: []string{"subnet-1"}, DeleteProtection: true,
	})
	if err != nil || fw.ARN == "" || fw.Status != "READY" {
		t.Fatalf("CreateFirewall: %v %+v", err, fw)
	}

	if _, err := m.CreateFirewall(ctx, nfdriver.CreateFirewallConfig{Name: "fw-1"}); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate firewall: got %v, want AlreadyExists", err)
	}

	// Delete protection blocks deletion.
	if _, err := m.DeleteFirewall(ctx, "fw-1", ""); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("delete protected firewall: got %v, want FailedPrecondition", err)
	}

	// Lookup by ARN works too.
	got, err := m.DescribeFirewall(ctx, "", fw.ARN)
	if err != nil || got.Name != "fw-1" {
		t.Fatalf("DescribeFirewall by ARN: %v %+v", err, got)
	}

	list, _ := m.ListFirewalls(ctx)
	if len(list) != 1 {
		t.Fatalf("ListFirewalls: %+v", list)
	}
}

func TestFirewallPolicyAndRuleGroup(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	pol, err := m.CreateFirewallPolicy(ctx, nfdriver.CreateFirewallPolicyConfig{
		Name: "pol-1", StatelessDefaultActions: []string{"aws:forward_to_sfe"},
	})
	if err != nil || pol.ID == "" {
		t.Fatalf("CreateFirewallPolicy: %v %+v", err, pol)
	}

	if _, err := m.DescribeFirewallPolicy(ctx, "pol-1", ""); err != nil {
		t.Fatalf("DescribeFirewallPolicy: %v", err)
	}

	if _, err := m.DeleteFirewallPolicy(ctx, "pol-1", ""); err != nil {
		t.Fatalf("DeleteFirewallPolicy: %v", err)
	}

	// Rule group: type is validated.
	if _, err := m.CreateRuleGroup(ctx, nfdriver.CreateRuleGroupConfig{Name: "rg", Type: "BOGUS"}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("bad rule group type: got %v, want InvalidArgument", err)
	}

	rg, err := m.CreateRuleGroup(ctx, nfdriver.CreateRuleGroupConfig{Name: "rg-1", Type: "STATEFUL", Capacity: 100})
	if err != nil {
		t.Fatalf("CreateRuleGroup: %v", err)
	}

	got, err := m.DescribeRuleGroup(ctx, "rg-1", "", "STATEFUL")
	if err != nil || got.Capacity != 100 {
		t.Fatalf("DescribeRuleGroup: %v %+v", err, got)
	}

	if _, err := m.DeleteRuleGroup(ctx, rg.Name, "", "STATEFUL"); err != nil {
		t.Fatalf("DeleteRuleGroup: %v", err)
	}

	if _, err := m.DescribeRuleGroup(ctx, "rg-1", "", "STATEFUL"); !cerrors.IsNotFound(err) {
		t.Fatalf("describe deleted rule group: got %v, want NotFound", err)
	}
}

func TestFirewallDepth(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	fw, err := m.CreateFirewall(ctx, nfdriver.CreateFirewallConfig{Name: "fw-1", SubnetIDs: []string{"subnet-1"}})
	if err != nil {
		t.Fatalf("CreateFirewall: %v", err)
	}

	if _, err := m.AssociateFirewallPolicy(ctx, "fw-1", "arn:policy"); err != nil {
		t.Fatalf("AssociateFirewallPolicy: %v", err)
	}

	assoc, err := m.AssociateSubnets(ctx, "fw-1", []string{"subnet-1", "subnet-2"})
	if err != nil || len(assoc.SubnetIDs) != 2 {
		t.Fatalf("AssociateSubnets: %v %+v", err, assoc)
	}

	dis, err := m.DisassociateSubnets(ctx, "fw-1", []string{"subnet-1"})
	if err != nil || len(dis.SubnetIDs) != 1 || dis.SubnetIDs[0] != "subnet-2" {
		t.Fatalf("DisassociateSubnets: %v %+v", err, dis)
	}

	prot, err := m.UpdateFirewallDeleteProtection(ctx, "fw-1", true)
	if err != nil || !prot.DeleteProtection {
		t.Fatalf("UpdateFirewallDeleteProtection: %v %+v", err, prot)
	}

	if err := m.UpdateLoggingConfiguration(ctx, "fw-1", []string{"FLOW", "ALERT"}); err != nil {
		t.Fatalf("UpdateLoggingConfiguration: %v", err)
	}

	logs, err := m.DescribeLoggingConfiguration(ctx, "fw-1")
	if err != nil || len(logs) != 2 {
		t.Fatalf("DescribeLoggingConfiguration: %v %+v", err, logs)
	}

	if err := m.TagResource(ctx, fw.ARN, map[string]string{"env": "prod"}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	got, _ := m.DescribeFirewall(ctx, "fw-1", "")
	if got.Tags["env"] != "prod" {
		t.Fatalf("tag not applied: %+v", got.Tags)
	}

	if err := m.UntagResource(ctx, fw.ARN, []string{"env"}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	got, _ = m.DescribeFirewall(ctx, "fw-1", "")
	if _, ok := got.Tags["env"]; ok {
		t.Fatalf("tag not removed: %+v", got.Tags)
	}

	// Unknown firewall / ARN error paths.
	if _, err := m.AssociateFirewallPolicy(ctx, "missing", "x"); !cerrors.IsNotFound(err) {
		t.Fatalf("associate missing firewall: got %v, want NotFound", err)
	}

	if err := m.TagResource(ctx, "arn:nope", map[string]string{"a": "b"}); !cerrors.IsNotFound(err) {
		t.Fatalf("tag unknown arn: got %v, want NotFound", err)
	}
}
