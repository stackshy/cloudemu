package topology

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/ec2"
	"github.com/stackshy/cloudemu/v2/providers/aws/route53"
	"github.com/stackshy/cloudemu/v2/providers/aws/vpc"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestEngine() (*Engine, *ec2.Mock, *vpc.Mock, *route53.Mock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	ec2Mock := ec2.New(opts)
	vpcMock := vpc.New(opts)
	dnsMock := route53.New(opts)
	engine := New(ec2Mock, vpcMock, dnsMock)

	return engine, ec2Mock, vpcMock, dnsMock
}

// attachIGW creates an internet gateway attached to vpcID and returns its id.
// CreateRoute validates its target exists, so route tests need a real gateway.
func attachIGW(t *testing.T, vpcMock *vpc.Mock, vpcID string) string {
	t.Helper()
	ctx := context.Background()

	igw, err := vpcMock.CreateInternetGateway(ctx, netdriver.InternetGatewayConfig{})
	require.NoError(t, err)
	require.NoError(t, vpcMock.AttachInternetGateway(ctx, igw.ID, vpcID))

	return igw.ID
}

// CIDR helper tests
func TestIPInCIDR(t *testing.T) {
	tests := []struct {
		name     string
		ip       string
		cidr     string
		expected bool
	}{
		{
			name:     "IP in large CIDR",
			ip:       "10.0.1.5",
			cidr:     "10.0.0.0/16",
			expected: true,
		},
		{
			name:     "IP outside small CIDR",
			ip:       "10.0.1.5",
			cidr:     "10.0.2.0/24",
			expected: false,
		},
		{
			name:     "0.0.0.0/0 matches all",
			ip:       "192.168.1.1",
			cidr:     "0.0.0.0/0",
			expected: true,
		},
		{
			name:     "invalid IP returns false",
			ip:       "not-an-ip",
			cidr:     "10.0.0.0/16",
			expected: false,
		},
		{
			name:     "invalid CIDR returns false",
			ip:       "10.0.1.5",
			cidr:     "bad-cidr",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ipInCIDR(tt.ip, tt.cidr)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPortInRange(t *testing.T) {
	tests := []struct {
		name     string
		port     int
		fromPort int
		toPort   int
		expected bool
	}{
		{
			name:     "exact match",
			port:     80,
			fromPort: 80,
			toPort:   80,
			expected: true,
		},
		{
			name:     "within range",
			port:     443,
			fromPort: 80,
			toPort:   8080,
			expected: true,
		},
		{
			name:     "outside range",
			port:     22,
			fromPort: 80,
			toPort:   443,
			expected: false,
		},
		{
			name:     "zero range matches all",
			port:     0,
			fromPort: 0,
			toPort:   0,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := portInRange(tt.port, tt.fromPort, tt.toPort)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestProtocolMatches(t *testing.T) {
	tests := []struct {
		name          string
		ruleProtocol  string
		queryProtocol string
		expected      bool
	}{
		{
			name:          "same protocol",
			ruleProtocol:  "tcp",
			queryProtocol: "tcp",
			expected:      true,
		},
		{
			name:          "wildcard rule matches any",
			ruleProtocol:  "-1",
			queryProtocol: "tcp",
			expected:      true,
		},
		{
			name:          "different protocols",
			ruleProtocol:  "tcp",
			queryProtocol: "udp",
			expected:      false,
		},
		{
			name:          "wildcard query matches any",
			ruleProtocol:  "udp",
			queryProtocol: "-1",
			expected:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := protocolMatches(tt.ruleProtocol, tt.queryProtocol)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFindMatchingRoute(t *testing.T) {
	routes := []netdriver.Route{
		{DestinationCIDR: "0.0.0.0/0", TargetID: "igw-1", TargetType: "gateway"},
		{DestinationCIDR: "10.0.0.0/16", TargetID: "local", TargetType: "local"},
		{DestinationCIDR: "10.0.1.0/24", TargetID: "nat-1", TargetType: "nat-gateway"},
	}

	tests := []struct {
		name           string
		destIP         string
		expectedTarget string
	}{
		{
			name:           "longest prefix wins /24 over /16 and /0",
			destIP:         "10.0.1.5",
			expectedTarget: "nat-1",
		},
		{
			name:           "/16 wins over /0",
			destIP:         "10.0.2.5",
			expectedTarget: "local",
		},
		{
			name:           "default route catches external",
			destIP:         "8.8.8.8",
			expectedTarget: "igw-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched := findMatchingRoute(routes, tt.destIP)
			require.NotNil(t, matched)
			assert.Equal(t, tt.expectedTarget, matched.TargetID)
		})
	}
}

// Security tests
func TestEvaluateSecurityGroupsAllowed(t *testing.T) {
	engine, _, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	v, err := vpcMock.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	srcSG, err := vpcMock.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{
		VPCID: v.ID, Name: "src-sg", Description: "source",
	})
	require.NoError(t, err)

	err = vpcMock.AddEgressRule(ctx, srcSG.ID, netdriver.SecurityRule{
		Protocol: "-1", FromPort: 0, ToPort: 0, CIDR: "0.0.0.0/0",
	})
	require.NoError(t, err)

	dstSG, err := vpcMock.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{
		VPCID: v.ID, Name: "dst-sg", Description: "destination",
	})
	require.NoError(t, err)

	err = vpcMock.AddIngressRule(ctx, dstSG.ID, netdriver.SecurityRule{
		Protocol: "tcp", FromPort: 443, ToPort: 443, CIDR: "0.0.0.0/0",
	})
	require.NoError(t, err)

	verdict, err := engine.EvaluateSecurityGroups(ctx, srcSG.ID, dstSG.ID, 443, "tcp")
	require.NoError(t, err)
	assert.True(t, verdict.Allowed)
	assert.NotNil(t, verdict.EgressMatch)
	assert.NotNil(t, verdict.IngressMatch)
}

func TestEvaluateSecurityGroupsBlockedByIngress(t *testing.T) {
	engine, _, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	v, err := vpcMock.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	srcSG, err := vpcMock.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{
		VPCID: v.ID, Name: "src-sg", Description: "source",
	})
	require.NoError(t, err)

	err = vpcMock.AddEgressRule(ctx, srcSG.ID, netdriver.SecurityRule{
		Protocol: "-1", FromPort: 0, ToPort: 0, CIDR: "0.0.0.0/0",
	})
	require.NoError(t, err)

	// Destination SG with NO ingress rules.
	dstSG, err := vpcMock.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{
		VPCID: v.ID, Name: "dst-sg", Description: "destination",
	})
	require.NoError(t, err)

	verdict, err := engine.EvaluateSecurityGroups(ctx, srcSG.ID, dstSG.ID, 443, "tcp")
	require.NoError(t, err)
	assert.False(t, verdict.Allowed)
	assert.Contains(t, verdict.Reason, "no ingress rule")
}

// A security group whose ingress rule references the source group (the common
// "allow db ingress from the app SG" pattern) must be honored. Before the fix
// the matcher looked only at CIDR and reported a false DENY.
func TestEvaluateSecurityGroupsReferencedGroupAllowed(t *testing.T) {
	engine, _, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	v, err := vpcMock.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	srcSG, err := vpcMock.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{
		VPCID: v.ID, Name: "app-sg", Description: "app",
	})
	require.NoError(t, err)

	err = vpcMock.AddEgressRule(ctx, srcSG.ID, netdriver.SecurityRule{
		Protocol: "-1", FromPort: 0, ToPort: 0, CIDR: "0.0.0.0/0",
	})
	require.NoError(t, err)

	dstSG, err := vpcMock.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{
		VPCID: v.ID, Name: "db-sg", Description: "db",
	})
	require.NoError(t, err)

	// Ingress allowed from the app SG by reference — no CIDR at all.
	err = vpcMock.AddIngressRule(ctx, dstSG.ID, netdriver.SecurityRule{
		Protocol: "tcp", FromPort: 5432, ToPort: 5432, ReferencedGroupID: srcSG.ID,
	})
	require.NoError(t, err)

	verdict, err := engine.EvaluateSecurityGroups(ctx, srcSG.ID, dstSG.ID, 5432, "tcp")
	require.NoError(t, err)
	assert.True(t, verdict.Allowed, "SG-to-SG reference must be honored")
	assert.NotNil(t, verdict.IngressMatch)
	assert.Equal(t, dstSG.ID, verdict.IngressMatch.GroupID)
}

// An ingress rule that references a different group must NOT admit a source
// that is not a member of that group.
func TestEvaluateSecurityGroupsReferencedGroupDenied(t *testing.T) {
	engine, _, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	v, err := vpcMock.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	srcSG, err := vpcMock.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{
		VPCID: v.ID, Name: "app-sg", Description: "app",
	})
	require.NoError(t, err)

	err = vpcMock.AddEgressRule(ctx, srcSG.ID, netdriver.SecurityRule{
		Protocol: "-1", FromPort: 0, ToPort: 0, CIDR: "0.0.0.0/0",
	})
	require.NoError(t, err)

	otherSG, err := vpcMock.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{
		VPCID: v.ID, Name: "other-sg", Description: "unrelated",
	})
	require.NoError(t, err)

	dstSG, err := vpcMock.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{
		VPCID: v.ID, Name: "db-sg", Description: "db",
	})
	require.NoError(t, err)

	// Ingress allowed only from otherSG — the source (srcSG) is not a member.
	err = vpcMock.AddIngressRule(ctx, dstSG.ID, netdriver.SecurityRule{
		Protocol: "tcp", FromPort: 5432, ToPort: 5432, ReferencedGroupID: otherSG.ID,
	})
	require.NoError(t, err)

	verdict, err := engine.EvaluateSecurityGroups(ctx, srcSG.ID, dstSG.ID, 5432, "tcp")
	require.NoError(t, err)
	assert.False(t, verdict.Allowed)
	assert.Contains(t, verdict.Reason, "no ingress rule")
}

// matchRules must honor each populated selector (CIDR, IPv6CIDR,
// ReferencedGroupID), never match on an empty/unpopulated selector, and treat
// an unresolvable prefix-list rule as non-matching (documented limitation).
func TestRuleSelectorMatching(t *testing.T) {
	tests := []struct {
		name  string
		rule  netdriver.SecurityRule
		src   ruleSource
		match bool
	}{
		{
			name:  "IPv4 CIDR matches",
			rule:  netdriver.SecurityRule{Protocol: "tcp", FromPort: 443, ToPort: 443, CIDR: "10.0.0.0/16"},
			src:   ruleSource{ip: "10.0.1.5"},
			match: true,
		},
		{
			name:  "IPv4 CIDR outside range",
			rule:  netdriver.SecurityRule{Protocol: "tcp", FromPort: 443, ToPort: 443, CIDR: "10.0.0.0/16"},
			src:   ruleSource{ip: "192.168.1.1"},
			match: false,
		},
		{
			name:  "IPv6 CIDR matches IPv6 source",
			rule:  netdriver.SecurityRule{Protocol: "tcp", FromPort: 443, ToPort: 443, IPv6CIDR: "2001:db8::/32"},
			src:   ruleSource{ip: "2001:db8::1"},
			match: true,
		},
		{
			name:  "IPv6 CIDR does not match outside source",
			rule:  netdriver.SecurityRule{Protocol: "tcp", FromPort: 443, ToPort: 443, IPv6CIDR: "2001:db8::/32"},
			src:   ruleSource{ip: "2001:dead::1"},
			match: false,
		},
		{
			name:  "referenced group matches member",
			rule:  netdriver.SecurityRule{Protocol: "tcp", FromPort: 443, ToPort: 443, ReferencedGroupID: "sg-app"},
			src:   ruleSource{ip: "10.0.1.5", groupIDs: []string{"sg-app", "sg-shared"}},
			match: true,
		},
		{
			name:  "referenced group does not match non-member",
			rule:  netdriver.SecurityRule{Protocol: "tcp", FromPort: 443, ToPort: 443, ReferencedGroupID: "sg-app"},
			src:   ruleSource{ip: "10.0.1.5", groupIDs: []string{"sg-other"}},
			match: false,
		},
		{
			name:  "empty selector never matches",
			rule:  netdriver.SecurityRule{Protocol: "tcp", FromPort: 443, ToPort: 443},
			src:   ruleSource{ip: "10.0.1.5", groupIDs: []string{"sg-app"}},
			match: false,
		},
		{
			name:  "prefix-list rule is unresolvable and does not match",
			rule:  netdriver.SecurityRule{Protocol: "tcp", FromPort: 443, ToPort: 443, PrefixListID: "pl-1234"},
			src:   ruleSource{ip: "10.0.1.5"},
			match: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			match := matchRules([]netdriver.SecurityRule{tt.rule}, "sg-test", 443, "tcp", tt.src)
			if tt.match {
				require.NotNil(t, match)
				assert.Equal(t, "sg-test", match.GroupID)
			} else {
				assert.Nil(t, match)
			}
		})
	}
}

func TestEvaluateNetworkACLAllow(t *testing.T) {
	engine, _, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	v, err := vpcMock.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	// A fresh custom ACL denies by default (only the '*' rules), so add an
	// explicit allow at rule 100; the matching allow should pass.
	acl, err := vpcMock.CreateNetworkACL(ctx, v.ID, nil)
	require.NoError(t, err)

	err = vpcMock.AddNetworkACLRule(ctx, acl.ID, &netdriver.NetworkACLRule{
		RuleNumber: 100, Protocol: "-1", Action: "allow", CIDR: "0.0.0.0/0", Egress: false,
	})
	require.NoError(t, err)

	verdict, err := engine.EvaluateNetworkACL(ctx, acl.ID, "10.0.1.5", "10.0.2.5", 443, "tcp", true)
	require.NoError(t, err)
	assert.True(t, verdict.Allowed)
	assert.Equal(t, 100, verdict.RuleNumber)
	assert.Equal(t, "allow", verdict.Action)
}

func TestEvaluateNetworkACLDenyBeforeAllow(t *testing.T) {
	engine, _, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	v, err := vpcMock.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	acl, err := vpcMock.CreateNetworkACL(ctx, v.ID, nil)
	require.NoError(t, err)

	// Add a deny rule at 50 (lower than the default allow at 100).
	err = vpcMock.AddNetworkACLRule(ctx, acl.ID, &netdriver.NetworkACLRule{
		RuleNumber: 50,
		Protocol:   "tcp",
		Action:     "deny",
		CIDR:       "0.0.0.0/0",
		FromPort:   443,
		ToPort:     443,
		Egress:     false,
	})
	require.NoError(t, err)

	verdict, err := engine.EvaluateNetworkACL(ctx, acl.ID, "10.0.1.5", "10.0.2.5", 443, "tcp", true)
	require.NoError(t, err)
	assert.False(t, verdict.Allowed)
	assert.Equal(t, 50, verdict.RuleNumber)
	assert.Equal(t, "deny", verdict.Action)
}

// CanConnect tests
// createVPCWithSubnetAndSGs is a helper that creates a VPC, a subnet, and two
// security groups. It returns the IDs needed by CanConnect tests.
func createVPCWithSubnetAndSGs(
	t *testing.T,
	ctx context.Context,
	vpcMock *vpc.Mock,
	cidr string,
	addIngressRule bool,
) (vpcID, subnetID, srcSGID, dstSGID string) {
	t.Helper()

	v, err := vpcMock.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: cidr})
	require.NoError(t, err)

	subnet, err := vpcMock.CreateSubnet(ctx, netdriver.SubnetConfig{
		VPCID: v.ID, CIDRBlock: cidr, AvailabilityZone: "us-east-1a",
	})
	require.NoError(t, err)

	srcSG, err := vpcMock.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{
		VPCID: v.ID, Name: "src-sg-" + v.ID, Description: "source",
	})
	require.NoError(t, err)

	err = vpcMock.AddEgressRule(ctx, srcSG.ID, netdriver.SecurityRule{
		Protocol: "-1", FromPort: 0, ToPort: 0, CIDR: "0.0.0.0/0",
	})
	require.NoError(t, err)

	dstSG, err := vpcMock.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{
		VPCID: v.ID, Name: "dst-sg-" + v.ID, Description: "destination",
	})
	require.NoError(t, err)

	if addIngressRule {
		err = vpcMock.AddIngressRule(ctx, dstSG.ID, netdriver.SecurityRule{
			Protocol: "tcp", FromPort: 443, ToPort: 443, CIDR: "0.0.0.0/0",
		})
		require.NoError(t, err)
	}

	return v.ID, subnet.ID, srcSG.ID, dstSG.ID
}

func TestCanConnectSameVPC(t *testing.T) {
	engine, ec2Mock, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	vpcID, subnetID, srcSGID, dstSGID := createVPCWithSubnetAndSGs(t, ctx, vpcMock, "10.0.0.0/16", true)

	srcInstances, err := ec2Mock.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-test", InstanceType: "t2.micro",
		SubnetID: subnetID, SecurityGroups: []string{srcSGID},
	}, 1)
	require.NoError(t, err)

	err = ec2Mock.SetInstanceVPC(srcInstances[0].ID, vpcID)
	require.NoError(t, err)

	dstInstances, err := ec2Mock.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-test", InstanceType: "t2.micro",
		SubnetID: subnetID, SecurityGroups: []string{dstSGID},
	}, 1)
	require.NoError(t, err)

	err = ec2Mock.SetInstanceVPC(dstInstances[0].ID, vpcID)
	require.NoError(t, err)

	result, err := engine.CanConnect(ctx, ConnectivityQuery{
		SrcInstanceID: srcInstances[0].ID,
		DstInstanceID: dstInstances[0].ID,
		Port:          443,
		Protocol:      "tcp",
	})
	require.NoError(t, err)
	assert.True(t, result.Allowed)
	assert.Equal(t, "traffic allowed", result.Reason)
	assert.True(t, result.SGVerdict.Allowed)
	assert.NotEmpty(t, result.Path)
}

func TestCanConnectBlockedBySG(t *testing.T) {
	engine, ec2Mock, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	vpcID, subnetID, srcSGID, dstSGID := createVPCWithSubnetAndSGs(t, ctx, vpcMock, "10.0.0.0/16", false)

	srcInstances, err := ec2Mock.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-test", InstanceType: "t2.micro",
		SubnetID: subnetID, SecurityGroups: []string{srcSGID},
	}, 1)
	require.NoError(t, err)

	err = ec2Mock.SetInstanceVPC(srcInstances[0].ID, vpcID)
	require.NoError(t, err)

	dstInstances, err := ec2Mock.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-test", InstanceType: "t2.micro",
		SubnetID: subnetID, SecurityGroups: []string{dstSGID},
	}, 1)
	require.NoError(t, err)

	err = ec2Mock.SetInstanceVPC(dstInstances[0].ID, vpcID)
	require.NoError(t, err)

	result, err := engine.CanConnect(ctx, ConnectivityQuery{
		SrcInstanceID: srcInstances[0].ID,
		DstInstanceID: dstInstances[0].ID,
		Port:          443,
		Protocol:      "tcp",
	})
	require.NoError(t, err)
	assert.False(t, result.Allowed)
	assert.Contains(t, result.Reason, "no ingress rule")
}

// A destination instance whose SG allows ingress from the source instance's SG
// (by reference, not by CIDR) must be reachable. This is the single most common
// real-world SG pattern; before the fix CanConnect returned a false DENY.
func TestCanConnectReferencedGroupAllowed(t *testing.T) {
	engine, ec2Mock, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	vpcID, subnetID, srcSGID, dstSGID := createVPCWithSubnetAndSGs(t, ctx, vpcMock, "10.0.0.0/16", false)

	// dst allows ingress from the source SG by reference — no CIDR.
	err := vpcMock.AddIngressRule(ctx, dstSGID, netdriver.SecurityRule{
		Protocol: "tcp", FromPort: 443, ToPort: 443, ReferencedGroupID: srcSGID,
	})
	require.NoError(t, err)

	srcInstances, err := ec2Mock.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-test", InstanceType: "t2.micro",
		SubnetID: subnetID, SecurityGroups: []string{srcSGID},
	}, 1)
	require.NoError(t, err)
	require.NoError(t, ec2Mock.SetInstanceVPC(srcInstances[0].ID, vpcID))

	dstInstances, err := ec2Mock.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-test", InstanceType: "t2.micro",
		SubnetID: subnetID, SecurityGroups: []string{dstSGID},
	}, 1)
	require.NoError(t, err)
	require.NoError(t, ec2Mock.SetInstanceVPC(dstInstances[0].ID, vpcID))

	result, err := engine.CanConnect(ctx, ConnectivityQuery{
		SrcInstanceID: srcInstances[0].ID,
		DstInstanceID: dstInstances[0].ID,
		Port:          443,
		Protocol:      "tcp",
	})
	require.NoError(t, err)
	assert.True(t, result.Allowed, "referenced-group ingress must allow a member source")
	assert.True(t, result.SGVerdict.Allowed)
}

// A source instance that is not a member of the referenced group must be denied.
func TestCanConnectReferencedGroupNotMember(t *testing.T) {
	engine, ec2Mock, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	vpcID, subnetID, srcSGID, dstSGID := createVPCWithSubnetAndSGs(t, ctx, vpcMock, "10.0.0.0/16", false)

	// A third group the source does not belong to.
	otherSG, err := vpcMock.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{
		VPCID: vpcID, Name: "other-sg", Description: "unrelated",
	})
	require.NoError(t, err)

	err = vpcMock.AddIngressRule(ctx, dstSGID, netdriver.SecurityRule{
		Protocol: "tcp", FromPort: 443, ToPort: 443, ReferencedGroupID: otherSG.ID,
	})
	require.NoError(t, err)

	srcInstances, err := ec2Mock.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-test", InstanceType: "t2.micro",
		SubnetID: subnetID, SecurityGroups: []string{srcSGID},
	}, 1)
	require.NoError(t, err)
	require.NoError(t, ec2Mock.SetInstanceVPC(srcInstances[0].ID, vpcID))

	dstInstances, err := ec2Mock.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-test", InstanceType: "t2.micro",
		SubnetID: subnetID, SecurityGroups: []string{dstSGID},
	}, 1)
	require.NoError(t, err)
	require.NoError(t, ec2Mock.SetInstanceVPC(dstInstances[0].ID, vpcID))

	result, err := engine.CanConnect(ctx, ConnectivityQuery{
		SrcInstanceID: srcInstances[0].ID,
		DstInstanceID: dstInstances[0].ID,
		Port:          443,
		Protocol:      "tcp",
	})
	require.NoError(t, err)
	assert.False(t, result.Allowed)
	assert.Contains(t, result.Reason, "no ingress rule")
}

func TestCanConnectCrossVPCPeering(t *testing.T) {
	engine, ec2Mock, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	vpc1ID, subnet1ID, srcSGID, _ := createVPCWithSubnetAndSGs(t, ctx, vpcMock, "10.0.0.0/16", true)
	vpc2ID, subnet2ID, _, dstSGID := createVPCWithSubnetAndSGs(t, ctx, vpcMock, "10.1.0.0/16", true)

	// Create and accept peering between the two VPCs.
	peering, err := vpcMock.CreatePeeringConnection(ctx, netdriver.PeeringConfig{
		RequesterVPC: vpc1ID, AccepterVPC: vpc2ID,
	})
	require.NoError(t, err)

	err = vpcMock.AcceptPeeringConnection(ctx, peering.ID)
	require.NoError(t, err)

	srcInstances, err := ec2Mock.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-test", InstanceType: "t2.micro",
		SubnetID: subnet1ID, SecurityGroups: []string{srcSGID},
	}, 1)
	require.NoError(t, err)

	err = ec2Mock.SetInstanceVPC(srcInstances[0].ID, vpc1ID)
	require.NoError(t, err)

	dstInstances, err := ec2Mock.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-test", InstanceType: "t2.micro",
		SubnetID: subnet2ID, SecurityGroups: []string{dstSGID},
	}, 1)
	require.NoError(t, err)

	err = ec2Mock.SetInstanceVPC(dstInstances[0].ID, vpc2ID)
	require.NoError(t, err)

	result, err := engine.CanConnect(ctx, ConnectivityQuery{
		SrcInstanceID: srcInstances[0].ID,
		DstInstanceID: dstInstances[0].ID,
		Port:          443,
		Protocol:      "tcp",
	})
	require.NoError(t, err)
	assert.True(t, result.Allowed)
}

func TestCanConnectCrossVPCNoPeering(t *testing.T) {
	engine, ec2Mock, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	vpc1ID, subnet1ID, srcSGID, _ := createVPCWithSubnetAndSGs(t, ctx, vpcMock, "10.0.0.0/16", true)
	vpc2ID, subnet2ID, _, dstSGID := createVPCWithSubnetAndSGs(t, ctx, vpcMock, "10.1.0.0/16", true)

	srcInstances, err := ec2Mock.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-test", InstanceType: "t2.micro",
		SubnetID: subnet1ID, SecurityGroups: []string{srcSGID},
	}, 1)
	require.NoError(t, err)

	err = ec2Mock.SetInstanceVPC(srcInstances[0].ID, vpc1ID)
	require.NoError(t, err)

	dstInstances, err := ec2Mock.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-test", InstanceType: "t2.micro",
		SubnetID: subnet2ID, SecurityGroups: []string{dstSGID},
	}, 1)
	require.NoError(t, err)

	err = ec2Mock.SetInstanceVPC(dstInstances[0].ID, vpc2ID)
	require.NoError(t, err)

	result, err := engine.CanConnect(ctx, ConnectivityQuery{
		SrcInstanceID: srcInstances[0].ID,
		DstInstanceID: dstInstances[0].ID,
		Port:          443,
		Protocol:      "tcp",
	})
	require.NoError(t, err)
	assert.False(t, result.Allowed)
	assert.Contains(t, result.Reason, "no active peering")
}

func TestCanConnectInstanceNotRunning(t *testing.T) {
	engine, ec2Mock, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	vpcID, subnetID, srcSGID, dstSGID := createVPCWithSubnetAndSGs(t, ctx, vpcMock, "10.0.0.0/16", true)

	srcInstances, err := ec2Mock.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-test", InstanceType: "t2.micro",
		SubnetID: subnetID, SecurityGroups: []string{srcSGID},
	}, 1)
	require.NoError(t, err)

	err = ec2Mock.SetInstanceVPC(srcInstances[0].ID, vpcID)
	require.NoError(t, err)

	dstInstances, err := ec2Mock.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-test", InstanceType: "t2.micro",
		SubnetID: subnetID, SecurityGroups: []string{dstSGID},
	}, 1)
	require.NoError(t, err)

	err = ec2Mock.SetInstanceVPC(dstInstances[0].ID, vpcID)
	require.NoError(t, err)

	// Stop the source instance.
	err = ec2Mock.StopInstances(ctx, []string{srcInstances[0].ID})
	require.NoError(t, err)

	_, err = engine.CanConnect(ctx, ConnectivityQuery{
		SrcInstanceID: srcInstances[0].ID,
		DstInstanceID: dstInstances[0].ID,
		Port:          443,
		Protocol:      "tcp",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not running")
}

// TraceRoute tests
func TestTraceRoute(t *testing.T) {
	engine, ec2Mock, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	v, err := vpcMock.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	subnet, err := vpcMock.CreateSubnet(ctx, netdriver.SubnetConfig{
		VPCID: v.ID, CIDRBlock: "10.0.1.0/24", AvailabilityZone: "us-east-1a",
	})
	require.NoError(t, err)

	rt, err := vpcMock.CreateRouteTable(ctx, netdriver.RouteTableConfig{VPCID: v.ID})
	require.NoError(t, err)

	igwID := attachIGW(t, vpcMock, v.ID)
	err = vpcMock.CreateRoute(ctx, rt.ID, "0.0.0.0/0", igwID, "gateway")
	require.NoError(t, err)

	// The subnet has to be associated with this table for its routes to
	// govern the subnet's traffic. Without the association the subnet uses the
	// VPC's main route table, which carries only the local route — so 8.8.8.8
	// would be genuinely unroutable, exactly as it would be in the real cloud.
	_, err = vpcMock.AssociateRouteTable(ctx, rt.ID, subnet.ID)
	require.NoError(t, err)

	sg, err := vpcMock.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{
		VPCID: v.ID, Name: "trace-sg", Description: "trace test",
	})
	require.NoError(t, err)

	instances, err := ec2Mock.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-test", InstanceType: "t2.micro",
		SubnetID: subnet.ID, SecurityGroups: []string{sg.ID},
	}, 1)
	require.NoError(t, err)

	err = ec2Mock.SetInstanceVPC(instances[0].ID, v.ID)
	require.NoError(t, err)

	hops, err := engine.TraceRoute(ctx, instances[0].ID, "8.8.8.8")
	require.NoError(t, err)
	require.NotEmpty(t, hops)

	// First hop: instance.
	assert.Equal(t, "instance", hops[0].Type)
	assert.Equal(t, instances[0].ID, hops[0].ResourceID)

	// Second hop: subnet.
	assert.Equal(t, "subnet", hops[1].Type)
	assert.Equal(t, subnet.ID, hops[1].ResourceID)

	// Third hop: route table.
	assert.Equal(t, "route-table", hops[2].Type)
	assert.Equal(t, rt.ID, hops[2].ResourceID)

	// Fourth hop: gateway via the 0.0.0.0/0 route.
	assert.Equal(t, "gateway", hops[3].Type)
	assert.Equal(t, igwID, hops[3].ResourceID)
}

// Resolve tests
func TestResolve(t *testing.T) {
	engine, _, _, dnsMock := newTestEngine()
	ctx := context.Background()

	zone, err := dnsMock.CreateZone(ctx, dnsdriver.ZoneConfig{Name: "example.com"})
	require.NoError(t, err)

	_, err = dnsMock.CreateRecord(ctx, dnsdriver.RecordConfig{
		ZoneID: zone.ID,
		Name:   "api.example.com",
		Type:   "A",
		TTL:    300,
		Values: []string{"1.2.3.4"},
	})
	require.NoError(t, err)

	values, err := engine.Resolve(ctx, "api.example.com")
	require.NoError(t, err)
	require.Len(t, values, 1)
	assert.Equal(t, "1.2.3.4", values[0])
}

func TestResolveNotFound(t *testing.T) {
	engine, _, _, dnsMock := newTestEngine()
	ctx := context.Background()

	_, err := dnsMock.CreateZone(ctx, dnsdriver.ZoneConfig{Name: "example.com"})
	require.NoError(t, err)

	values, err := engine.Resolve(ctx, "missing.example.com")
	require.NoError(t, err)
	assert.Nil(t, values)
}

// A subnet with no explicit association uses the VPC's main route table, which
// carries only the local route. Traffic off the VPC is then genuinely
// unroutable — and reporting otherwise would tell a caller a path exists that
// the real cloud would drop.
func TestTraceRouteUnassociatedSubnetUsesMainTable(t *testing.T) {
	engine, ec2Mock, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	v, err := vpcMock.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	subnet, err := vpcMock.CreateSubnet(ctx, netdriver.SubnetConfig{
		VPCID: v.ID, CIDRBlock: "10.0.1.0/24", AvailabilityZone: "us-east-1a",
	})
	require.NoError(t, err)

	// A table with a default route exists, but nothing associates the subnet
	// with it, so it does not govern this subnet's traffic.
	rt, err := vpcMock.CreateRouteTable(ctx, netdriver.RouteTableConfig{VPCID: v.ID})
	require.NoError(t, err)
	require.NoError(t, vpcMock.CreateRoute(ctx, rt.ID, "0.0.0.0/0", attachIGW(t, vpcMock, v.ID), "gateway"))

	sg, err := vpcMock.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{
		VPCID: v.ID, Name: "unassoc-sg", Description: "unassociated subnet",
	})
	require.NoError(t, err)

	instances, err := ec2Mock.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-test", InstanceType: "t2.micro",
		SubnetID: subnet.ID, SecurityGroups: []string{sg.ID},
	}, 1)
	require.NoError(t, err)
	require.NoError(t, ec2Mock.SetInstanceVPC(instances[0].ID, v.ID))

	hops, err := engine.TraceRoute(ctx, instances[0].ID, "8.8.8.8")
	require.NoError(t, err)
	require.NotEmpty(t, hops)

	assert.NotEqual(t, rt.ID, hops[2].ResourceID,
		"an unassociated table must not govern the subnet")
	assert.Equal(t, "blocked", hops[len(hops)-1].Type,
		"main table carries only the local route, so 8.8.8.8 is unroutable")
}
