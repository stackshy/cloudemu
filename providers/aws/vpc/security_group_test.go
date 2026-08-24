package vpc

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// sgWithRules creates a security group holding one ingress and one egress rule,
// each carrying a known sgr- id and a tag, and returns the group id.
func sgWithRules(t *testing.T, m *Mock) string {
	t.Helper()

	ctx := context.Background()
	v := createTestVPC(m)

	sg, err := m.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{Name: "sg", Description: "sg", VPCID: v.ID})
	requireNoError(t, err)

	requireNoError(t, m.AddIngressRule(ctx, sg.ID, driver.SecurityRule{
		Protocol: "tcp", FromPort: 22, ToPort: 22, CIDR: "0.0.0.0/0",
		Description: "old", RuleID: "sgr-ingress", Tags: map[string]string{"env": "test"},
	}))

	requireNoError(t, m.AddEgressRule(ctx, sg.ID, driver.SecurityRule{
		Protocol: "tcp", FromPort: 443, ToPort: 443, CIDR: "10.0.0.0/16",
		RuleID: "sgr-egress",
	}))

	return sg.ID
}

// ruleByID returns the ingress-or-egress rule with the given sgr- id.
func ruleByID(t *testing.T, m *Mock, groupID, ruleID string) driver.SecurityRule {
	t.Helper()

	sgs, err := m.DescribeSecurityGroups(context.Background(), []string{groupID})
	requireNoError(t, err)

	for _, r := range append(sgs[0].IngressRules, sgs[0].EgressRules...) {
		if r.RuleID == ruleID {
			return r
		}
	}

	t.Fatalf("rule %q not found in group %q", ruleID, groupID)

	return driver.SecurityRule{}
}

// TestModifySecurityGroupRulePreservesRuleIDAndTags covers the full-replace of a
// rule's permission fields while its RuleID and Tags are preserved.
func TestModifySecurityGroupRulePreservesRuleIDAndTags(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	groupID := sgWithRules(t, m)

	requireNoError(t, m.ModifySecurityGroupRule(ctx, groupID, "sgr-ingress", driver.SecurityRule{
		Protocol: "udp", FromPort: 53, ToPort: 53, CIDR: "192.168.0.0/24", Description: "dns",
	}))

	got := ruleByID(t, m, groupID, "sgr-ingress")
	assertEqual(t, "sgr-ingress", got.RuleID)
	assertEqual(t, "udp", got.Protocol)
	assertEqual(t, 53, got.FromPort)
	assertEqual(t, "192.168.0.0/24", got.CIDR)
	assertEqual(t, "dns", got.Description)
	assertEqual(t, "test", got.Tags["env"])
}

// TestModifySecurityGroupRuleNotFound covers the group- and rule-level
// not-found errors.
func TestModifySecurityGroupRuleNotFound(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	groupID := sgWithRules(t, m)

	if err := m.ModifySecurityGroupRule(ctx, "sg-missing", "sgr-ingress",
		driver.SecurityRule{CIDR: "0.0.0.0/0"}); !isNotFound(err) {
		t.Fatalf("missing group = %v, want NotFound", err)
	}

	if err := m.ModifySecurityGroupRule(ctx, groupID, "sgr-nope",
		driver.SecurityRule{CIDR: "0.0.0.0/0"}); !isNotFound(err) {
		t.Fatalf("missing rule = %v, want NotFound", err)
	}
}

// TestSetSecurityGroupRuleDescriptionByDirection covers setting/clearing a
// description on the correct list and the direction-mismatch miss (an egress id
// passed as ingress is not found).
func TestSetSecurityGroupRuleDescriptionByDirection(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	groupID := sgWithRules(t, m)

	requireNoError(t, m.SetSecurityGroupRuleDescription(ctx, groupID, "sgr-ingress", false, "ssh"))
	assertEqual(t, "ssh", ruleByID(t, m, groupID, "sgr-ingress").Description)

	// Empty clears it.
	requireNoError(t, m.SetSecurityGroupRuleDescription(ctx, groupID, "sgr-ingress", false, ""))
	assertEqual(t, "", ruleByID(t, m, groupID, "sgr-ingress").Description)

	// Direction is honored: an egress id searched on the ingress list misses.
	if err := m.SetSecurityGroupRuleDescription(ctx, groupID, "sgr-egress", false, "x"); !isNotFound(err) {
		t.Fatalf("egress id on ingress list = %v, want NotFound", err)
	}

	requireNoError(t, m.SetSecurityGroupRuleDescription(ctx, groupID, "sgr-egress", true, "internal"))
	assertEqual(t, "internal", ruleByID(t, m, groupID, "sgr-egress").Description)
}
