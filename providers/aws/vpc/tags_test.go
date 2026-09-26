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

// TestAddressingResourceTagger covers tagging Elastic IP allocations, VPC
// endpoints and VPC endpoint services after creation: the tag merges with the
// ones set at create time on the same record the Describe calls read, delete
// removes only the named key, and an unknown id of each prefix is NotFound.
func TestAddressingResourceTagger(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	v := createTestVPC(m)

	eip, err := m.AllocateAddress(ctx, driver.ElasticIPConfig{Tags: map[string]string{"Name": "eip"}})
	requireNoError(t, err)

	ep, err := m.CreateVPCEndpoint(ctx, driver.VPCEndpointConfig{
		VPCID: v.ID, ServiceName: "com.amazonaws.us-east-1.s3", EndpointType: "Gateway",
		Tags: map[string]string{"Name": "ep"},
	})
	requireNoError(t, err)

	svc, err := m.CreateVPCEndpointServiceConfiguration(ctx, driver.EndpointServiceConfig{
		NetworkLoadBalancerARNs: []string{"arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/net/n/1"},
	})
	requireNoError(t, err)

	for _, id := range []string{eip.AllocationID, ep.ID, svc.ID} {
		requireNoError(t, m.UpdateResourceTags(ctx, id, map[string]string{"env": "prod"}))
	}

	eips, err := m.DescribeAddresses(ctx, []string{eip.AllocationID})
	requireNoError(t, err)
	assertEqual(t, "prod", eips[0].Tags["env"])
	assertEqual(t, "eip", eips[0].Tags["Name"])

	eps, err := m.DescribeVPCEndpoints(ctx, []string{ep.ID})
	requireNoError(t, err)
	assertEqual(t, "prod", eps[0].Tags["env"])
	assertEqual(t, "ep", eps[0].Tags["Name"])

	svcs, err := m.DescribeVPCEndpointServiceConfigurations(ctx, []string{svc.ID})
	requireNoError(t, err)
	assertEqual(t, "prod", svcs[0].Tags["env"])

	for _, id := range []string{eip.AllocationID, ep.ID, svc.ID} {
		requireNoError(t, m.RemoveResourceTags(ctx, id, []string{"env"}))
	}

	eips, err = m.DescribeAddresses(ctx, []string{eip.AllocationID})
	requireNoError(t, err)

	if _, ok := eips[0].Tags["env"]; ok {
		t.Fatalf("eip still carries env after RemoveResourceTags: %v", eips[0].Tags)
	}

	assertEqual(t, "eip", eips[0].Tags["Name"])

	eps, err = m.DescribeVPCEndpoints(ctx, []string{ep.ID})
	requireNoError(t, err)

	if _, ok := eps[0].Tags["env"]; ok {
		t.Fatalf("endpoint still carries env after RemoveResourceTags: %v", eps[0].Tags)
	}

	for _, id := range []string{"eipalloc-missing", "vpce-missing", "vpce-svc-missing"} {
		if err := m.UpdateResourceTags(ctx, id, map[string]string{"k": "v"}); !isNotFound(err) {
			t.Fatalf("UpdateResourceTags(%s) = %v, want NotFound", id, err)
		}
	}
}
