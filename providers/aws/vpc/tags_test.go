package vpc

import (
	"context"
	"sync"
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

// TestRemoveResourceTagsWithoutKeysKeepsReservedTags pins EC2 DeleteTags with no
// Tag parameter: every user tag goes, the AWS-generated "aws:" tags stay. Before
// the fix an empty key list removed nothing.
func TestRemoveResourceTagsWithoutKeysKeepsReservedTags(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	eip, err := m.AllocateAddress(ctx, driver.ElasticIPConfig{Tags: map[string]string{
		"Name": "eip", "env": "prod", "aws:cloudformation:stack-name": "s",
	}})
	requireNoError(t, err)

	requireNoError(t, m.RemoveResourceTags(ctx, eip.AllocationID, nil))

	got, err := m.ResourceTags(ctx, eip.AllocationID)
	requireNoError(t, err)

	if len(got) != 1 || got["aws:cloudformation:stack-name"] != "s" {
		t.Fatalf("tags after RemoveResourceTags(nil) = %v, want only the aws: tag", got)
	}
}

// TestResourceTagsReadsEveryTaggableID covers ResourceTags on each id family the
// EC2 tag handler routes to this provider, that it returns a copy, and that an
// unknown id is NotFound.
func TestResourceTagsReadsEveryTaggableID(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	v, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16", Tags: map[string]string{"k": "vpc"}})
	requireNoError(t, err)

	s, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: v.ID, CIDRBlock: "10.0.1.0/24", Tags: map[string]string{"k": "subnet"}})
	requireNoError(t, err)

	sg, err := m.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{VPCID: v.ID, Name: "sg", Description: "d", Tags: map[string]string{"k": "sg"}})
	requireNoError(t, err)

	eip, err := m.AllocateAddress(ctx, driver.ElasticIPConfig{Tags: map[string]string{"k": "eip"}})
	requireNoError(t, err)

	rt, err := m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: v.ID})
	requireNoError(t, err)
	requireNoError(t, m.UpdateResourceTags(ctx, rt.ID, map[string]string{"k": "rtb"}))

	for id, want := range map[string]string{v.ID: "vpc", s.ID: "subnet", sg.ID: "sg", eip.AllocationID: "eip", rt.ID: "rtb"} {
		got, err := m.ResourceTags(ctx, id)
		requireNoError(t, err)
		assertEqual(t, want, got["k"])

		got["k"] = "mutated"

		again, err := m.ResourceTags(ctx, id)
		requireNoError(t, err)
		assertEqual(t, want, again["k"])
	}

	for _, id := range []string{"vpc-missing", "subnet-missing", "sg-missing", "eipalloc-missing", "rtb-missing", "vol-other"} {
		if _, err := m.ResourceTags(ctx, id); !isNotFound(err) {
			t.Fatalf("ResourceTags(%s) = %v, want NotFound", id, err)
		}
	}
}

// TestAddressingTagWriteDoesNotRaceDescribe pins the m.mu the tag writer holds
// for Elastic IPs and VPC endpoints: DescribeAddresses / DescribeVPCEndpoints
// read the record's Tags field under m.mu.RLock, so without the writer's lock
// this trips -race. ModifyVPCEndpoint writes the same record and is run too.
func TestAddressingTagWriteDoesNotRaceDescribe(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	v := createTestVPC(m)

	eip, err := m.AllocateAddress(ctx, driver.ElasticIPConfig{})
	requireNoError(t, err)

	ep, err := m.CreateVPCEndpoint(ctx, driver.VPCEndpointConfig{
		VPCID: v.ID, ServiceName: "com.amazonaws.us-east-1.s3", EndpointType: "Gateway",
	})
	requireNoError(t, err)

	const iters = 200

	var wg sync.WaitGroup

	wg.Add(4)

	go func() {
		defer wg.Done()

		for i := 0; i < iters; i++ {
			_ = m.UpdateResourceTags(ctx, eip.AllocationID, map[string]string{"k": "v"})
			_ = m.UpdateResourceTags(ctx, ep.ID, map[string]string{"k": "v"})
		}
	}()

	go func() {
		defer wg.Done()

		for i := 0; i < iters; i++ {
			_, _ = m.ModifyVPCEndpoint(ctx, ep.ID, driver.VPCEndpointConfig{
				RouteTableIDs: []string{"rtb-1"}, Tags: map[string]string{"m": "v"},
			})
		}
	}()

	go func() {
		defer wg.Done()

		for i := 0; i < iters; i++ {
			_, _ = m.DescribeAddresses(ctx, []string{eip.AllocationID})
		}
	}()

	go func() {
		defer wg.Done()

		for i := 0; i < iters; i++ {
			_, _ = m.DescribeVPCEndpoints(ctx, []string{ep.ID})
		}
	}()

	wg.Wait()
}
