package vpc

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// TestNetworkResourceTagger covers UpdateResourceTags/RemoveResourceTags for the
// VPC-family resources routed through the optional interface: the tag lands on
// the owning store, delete removes it, and an unknown id is NotFound.
func TestNetworkResourceTagger(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	v := createTestVPC(m)

	rt, err := m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: v.ID})
	requireNoError(t, err)

	igw, err := m.CreateInternetGateway(ctx, driver.InternetGatewayConfig{})
	requireNoError(t, err)

	dopt, err := m.CreateDHCPOptions(ctx, driver.DHCPOptionsConfig{
		Configuration: map[string][]string{"domain-name-servers": {"10.0.0.2"}},
	})
	requireNoError(t, err)

	for _, id := range []string{rt.ID, igw.ID, dopt.ID} {
		requireNoError(t, m.UpdateResourceTags(ctx, id, map[string]string{"Name": "n"}))
	}

	assertEqual(t, "n", routeTableTag(t, m, rt.ID, "Name"))

	requireNoError(t, m.RemoveResourceTags(ctx, rt.ID, []string{"Name"}))
	assertEqual(t, "", routeTableTag(t, m, rt.ID, "Name"))

	if err := m.UpdateResourceTags(ctx, "rtb-missing", map[string]string{"k": "v"}); !isNotFound(err) {
		t.Fatalf("UpdateResourceTags on missing id = %v, want NotFound", err)
	}

	if err := m.RemoveResourceTags(ctx, "vol-notnetwork", []string{"k"}); !isNotFound(err) {
		t.Fatalf("RemoveResourceTags on non-network id = %v, want NotFound", err)
	}
}

// TestSecurityGroupRuleTagger covers tagging a security-group rule by its sgr-
// id through the optional NetworkResourceTagger interface: the tag lands on the
// owning rule, delete removes the key, and an unknown sgr- id is NotFound.
func TestSecurityGroupRuleTagger(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	v := createTestVPC(m)

	sg, err := m.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{Name: "sg", Description: "sg", VPCID: v.ID})
	requireNoError(t, err)

	requireNoError(t, m.AddIngressRule(ctx, sg.ID, driver.SecurityRule{
		Protocol: "tcp", FromPort: 443, ToPort: 443, CIDR: "0.0.0.0/0", RuleID: "sgr-abc123",
	}))

	requireNoError(t, m.UpdateResourceTags(ctx, "sgr-abc123", map[string]string{"env": "prod"}))
	assertEqual(t, "prod", securityGroupRuleTag(t, m, sg.ID, "sgr-abc123", "env"))

	requireNoError(t, m.RemoveResourceTags(ctx, "sgr-abc123", []string{"env"}))
	assertEqual(t, "", securityGroupRuleTag(t, m, sg.ID, "sgr-abc123", "env"))

	if err := m.UpdateResourceTags(ctx, "sgr-missing", map[string]string{"k": "v"}); !isNotFound(err) {
		t.Fatalf("UpdateResourceTags on missing sgr- id = %v, want NotFound", err)
	}
}

func securityGroupRuleTag(t *testing.T, m *Mock, groupID, ruleID, key string) string {
	t.Helper()

	sgs, err := m.DescribeSecurityGroups(context.Background(), []string{groupID})
	requireNoError(t, err)

	for i := range sgs[0].IngressRules {
		if sgs[0].IngressRules[i].RuleID == ruleID {
			return sgs[0].IngressRules[i].Tags[key]
		}
	}

	t.Fatalf("rule %s not found in group %s", ruleID, groupID)

	return ""
}

func routeTableTag(t *testing.T, m *Mock, id, key string) string {
	t.Helper()

	rts, err := m.DescribeRouteTables(context.Background(), []string{id})
	requireNoError(t, err)

	return rts[0].Tags[key]
}

func isNotFound(err error) bool {
	return err != nil && errors.IsNotFound(err)
}
