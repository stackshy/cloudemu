package topology

import (
	"context"
	"testing"
	"time"

	computedriver "github.com/stackshy/cloudemu/compute/driver"
	"github.com/stackshy/cloudemu/config"
	dnsdriver "github.com/stackshy/cloudemu/dns/driver"
	lbdriver "github.com/stackshy/cloudemu/loadbalancer/driver"
	iamdriver "github.com/stackshy/cloudemu/iam/driver"
	monitoringdriver "github.com/stackshy/cloudemu/monitoring/driver"
	mqdriver "github.com/stackshy/cloudemu/messagequeue/driver"
	netdriver "github.com/stackshy/cloudemu/networking/driver"
	serverlessdriver "github.com/stackshy/cloudemu/serverless/driver"
	"github.com/stackshy/cloudemu/providers/aws/awsiam"
	"github.com/stackshy/cloudemu/providers/aws/cloudwatch"
	"github.com/stackshy/cloudemu/providers/aws/ec2"
	"github.com/stackshy/cloudemu/providers/aws/elb"
	"github.com/stackshy/cloudemu/providers/aws/lambda"
	"github.com/stackshy/cloudemu/providers/aws/route53"
	"github.com/stackshy/cloudemu/providers/aws/sqs"
	"github.com/stackshy/cloudemu/providers/aws/vpc"
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

func TestEvaluateNetworkACLAllow(t *testing.T) {
	engine, _, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	v, err := vpcMock.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	// CreateNetworkACL adds default allow-all at rule 100. That should pass.
	acl, err := vpcMock.CreateNetworkACL(ctx, v.ID, nil)
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

	err = vpcMock.CreateRoute(ctx, rt.ID, "0.0.0.0/0", "igw-123", "gateway")
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
	assert.Equal(t, "igw-123", hops[3].ResourceID)
}

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

// createGraphResources is a helper that creates a VPC, subnet, SG, and instance.
// It returns their IDs for graph verification.
func createGraphResources(
	t *testing.T,
	ctx context.Context,
	ec2Mock *ec2.Mock,
	vpcMock *vpc.Mock,
) (vpcID, subnetID, sgID, instanceID string) {
	t.Helper()

	v, err := vpcMock.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	subnet, err := vpcMock.CreateSubnet(ctx, netdriver.SubnetConfig{
		VPCID: v.ID, CIDRBlock: "10.0.1.0/24", AvailabilityZone: "us-east-1a",
	})
	require.NoError(t, err)

	sg, err := vpcMock.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{
		VPCID: v.ID, Name: "graph-sg", Description: "graph test",
	})
	require.NoError(t, err)

	instances, err := ec2Mock.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-test", InstanceType: "t2.micro",
		SubnetID: subnet.ID, SecurityGroups: []string{sg.ID},
	}, 1)
	require.NoError(t, err)

	err = ec2Mock.SetInstanceVPC(instances[0].ID, v.ID)
	require.NoError(t, err)

	return v.ID, subnet.ID, sg.ID, instances[0].ID
}

func TestBuildDependencyGraph(t *testing.T) {
	engine, ec2Mock, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	vpcID, subnetID, sgID, instanceID := createGraphResources(t, ctx, ec2Mock, vpcMock)

	graph, err := engine.BuildDependencyGraph(ctx)
	require.NoError(t, err)
	require.NotNil(t, graph)

	// Verify all resources are present.
	ids := resourceIDs(graph)
	assert.Contains(t, ids, vpcID)
	assert.Contains(t, ids, subnetID)
	assert.Contains(t, ids, sgID)
	assert.Contains(t, ids, instanceID)

	// Verify dependencies exist.
	assert.True(t, hasDependency(graph, subnetID, vpcID, "member-of"))
	assert.True(t, hasDependency(graph, sgID, vpcID, "member-of"))
	assert.True(t, hasDependency(graph, instanceID, vpcID, "member-of"))
	assert.True(t, hasDependency(graph, instanceID, subnetID, "member-of"))
	assert.True(t, hasDependency(graph, instanceID, sgID, "secured-by"))
}

func TestBlastRadius(t *testing.T) {
	engine, ec2Mock, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	vpcID, subnetID, sgID, instanceID := createGraphResources(t, ctx, ec2Mock, vpcMock)

	report, err := engine.BlastRadius(ctx, vpcID)
	require.NoError(t, err)
	require.NotNil(t, report)

	assert.Equal(t, vpcID, report.Target.ID)
	assert.Equal(t, "delete", report.Action)

	// Subnet and SG are direct dependents of VPC.
	directIDs := refIDs(report.DirectlyAffected)
	assert.Contains(t, directIDs, subnetID)
	assert.Contains(t, directIDs, sgID)

	// Instance depends on subnet and SG, so it should appear in transitive impact.
	allImpacted := append(refIDs(report.DirectlyAffected), refIDs(report.TransitiveImpact)...)
	assert.Contains(t, allImpacted, instanceID)
	assert.NotEmpty(t, report.BrokenConnections)
	assert.NotEmpty(t, report.Summary)
}

func TestDependsOn(t *testing.T) {
	engine, ec2Mock, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	vpcID, subnetID, sgID, instanceID := createGraphResources(t, ctx, ec2Mock, vpcMock)

	deps, err := engine.DependsOn(ctx, instanceID)
	require.NoError(t, err)

	depIDs := refIDs(deps)
	assert.Contains(t, depIDs, vpcID)
	assert.Contains(t, depIDs, subnetID)
	assert.Contains(t, depIDs, sgID)
}

func TestDependedBy(t *testing.T) {
	engine, ec2Mock, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	vpcID, subnetID, sgID, instanceID := createGraphResources(t, ctx, ec2Mock, vpcMock)

	dependents, err := engine.DependedBy(ctx, vpcID)
	require.NoError(t, err)

	depIDs := refIDs(dependents)
	assert.Contains(t, depIDs, subnetID)
	assert.Contains(t, depIDs, sgID)
	assert.Contains(t, depIDs, instanceID)
}

func TestExportDOT(t *testing.T) {
	engine, ec2Mock, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	vpcID, subnetID, _, _ := createGraphResources(t, ctx, ec2Mock, vpcMock)

	graph, err := engine.BuildDependencyGraph(ctx)
	require.NoError(t, err)

	dot := graph.ExportDOT()
	assert.Contains(t, dot, "digraph CloudEmu")
	assert.Contains(t, dot, vpcID)
	assert.Contains(t, dot, subnetID)
	assert.Contains(t, dot, "member-of")
}

func TestExportMermaid(t *testing.T) {
	engine, ec2Mock, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	vpcID, subnetID, _, _ := createGraphResources(t, ctx, ec2Mock, vpcMock)

	graph, err := engine.BuildDependencyGraph(ctx)
	require.NoError(t, err)

	mermaid := graph.ExportMermaid()
	assert.Contains(t, mermaid, "graph TD")
	assert.Contains(t, mermaid, vpcID)
	assert.Contains(t, mermaid, subnetID)
	assert.Contains(t, mermaid, "member-of")
}

func TestBlastRadiusNotFound(t *testing.T) {
	engine, _, _, _ := newTestEngine()
	ctx := context.Background()

	_, err := engine.BlastRadius(ctx, "nonexistent-resource")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func resourceIDs(g *DependencyGraph) []string {
	ids := make([]string, 0, len(g.Resources))
	for _, r := range g.Resources {
		ids = append(ids, r.ID)
	}

	return ids
}

func refIDs(refs []ResourceRef) []string {
	ids := make([]string, 0, len(refs))
	for _, r := range refs {
		ids = append(ids, r.ID)
	}

	return ids
}

func hasDependency(g *DependencyGraph, fromID, toID, depType string) bool {
	for _, d := range g.Dependencies {
		if d.From.ID == fromID && d.To.ID == toID && d.Type == depType {
			return true
		}
	}

	return false
}

func hasResourceType(g *DependencyGraph, resType string) bool {
	for _, r := range g.Resources {
		if r.Type == resType {
			return true
		}
	}

	return false
}

// newTestOpts returns shared config options for tests.
func newTestOpts() *config.Options {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	return config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
}

func TestVolumeInGraph(t *testing.T) {
	engine, ec2Mock, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	vpcID, subnetID, sgID, instanceID := createGraphResources(t, ctx, ec2Mock, vpcMock)
	_ = vpcID
	_ = subnetID
	_ = sgID

	vol, err := ec2Mock.CreateVolume(ctx, computedriver.VolumeConfig{
		Size: 100, VolumeType: "gp2", AvailabilityZone: "us-east-1a",
	})
	require.NoError(t, err)

	err = ec2Mock.AttachVolume(ctx, vol.ID, instanceID, "/dev/sdf")
	require.NoError(t, err)

	graph, err := engine.BuildDependencyGraph(ctx)
	require.NoError(t, err)

	assert.Contains(t, resourceIDs(graph), vol.ID)
	assert.True(t, hasDependency(graph, vol.ID, instanceID, "attached-to"))
}

func TestBlastRadiusWithVolumes(t *testing.T) {
	engine, ec2Mock, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	_, _, _, instanceID := createGraphResources(t, ctx, ec2Mock, vpcMock)

	vol, err := ec2Mock.CreateVolume(ctx, computedriver.VolumeConfig{
		Size: 50, VolumeType: "gp2", AvailabilityZone: "us-east-1a",
	})
	require.NoError(t, err)

	err = ec2Mock.AttachVolume(ctx, vol.ID, instanceID, "/dev/sdf")
	require.NoError(t, err)

	report, err := engine.BlastRadius(ctx, instanceID)
	require.NoError(t, err)

	// The volume depends on the instance, so it's a direct dependent.
	directIDs := refIDs(report.DirectlyAffected)
	assert.Contains(t, directIDs, vol.ID)
}

func TestLBChainGraph(t *testing.T) {
	opts := newTestOpts()
	ec2Mock := ec2.New(opts)
	vpcMock := vpc.New(opts)
	dnsMock := route53.New(opts)
	elbMock := elb.New(opts)
	engine := New(ec2Mock, vpcMock, dnsMock, WithLoadBalancer(elbMock))
	ctx := context.Background()

	v, err := vpcMock.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	subnet, err := vpcMock.CreateSubnet(ctx, netdriver.SubnetConfig{
		VPCID: v.ID, CIDRBlock: "10.0.1.0/24", AvailabilityZone: "us-east-1a",
	})
	require.NoError(t, err)

	sg, err := vpcMock.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{
		VPCID: v.ID, Name: "lb-sg", Description: "lb test",
	})
	require.NoError(t, err)

	instances, err := ec2Mock.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-test", InstanceType: "t2.micro",
		SubnetID: subnet.ID, SecurityGroups: []string{sg.ID},
	}, 1)
	require.NoError(t, err)

	err = ec2Mock.SetInstanceVPC(instances[0].ID, v.ID)
	require.NoError(t, err)

	lb, err := elbMock.CreateLoadBalancer(ctx, lbdriver.LBConfig{
		Name: "test-lb", Type: "application", Scheme: "internal",
		Subnets: []string{subnet.ID},
	})
	require.NoError(t, err)

	tg, err := elbMock.CreateTargetGroup(ctx, lbdriver.TargetGroupConfig{
		Name: "test-tg", Protocol: "HTTP", Port: 80, VPCID: v.ID,
	})
	require.NoError(t, err)

	lis, err := elbMock.CreateListener(ctx, lbdriver.ListenerConfig{
		LBARN: lb.ARN, Protocol: "HTTP", Port: 80, TargetGroupARN: tg.ARN,
	})
	require.NoError(t, err)

	err = elbMock.RegisterTargets(ctx, tg.ARN, []lbdriver.Target{
		{ID: instances[0].ID, Port: 80},
	})
	require.NoError(t, err)

	graph, err := engine.BuildDependencyGraph(ctx)
	require.NoError(t, err)

	// Verify LB chain resources exist.
	ids := resourceIDs(graph)
	assert.Contains(t, ids, lb.ARN)
	assert.Contains(t, ids, tg.ARN)
	assert.Contains(t, ids, lis.ARN)

	// Verify LB chain dependencies.
	assert.True(t, hasDependency(graph, lb.ARN, subnet.ID, "member-of"))
	assert.True(t, hasDependency(graph, tg.ARN, v.ID, "member-of"))
	assert.True(t, hasDependency(graph, lis.ARN, lb.ARN, "belongs-to"))
	assert.True(t, hasDependency(graph, lis.ARN, tg.ARN, "routes-to"))
	assert.True(t, hasDependency(graph, instances[0].ID, tg.ARN, "member-of"))
}

func TestFunctionESMGraph(t *testing.T) {
	opts := newTestOpts()
	ec2Mock := ec2.New(opts)
	vpcMock := vpc.New(opts)
	dnsMock := route53.New(opts)
	lambdaMock := lambda.New(opts)
	sqsMock := sqs.New(opts)
	engine := New(ec2Mock, vpcMock, dnsMock,
		WithServerless(lambdaMock),
		WithMessageQueue(sqsMock),
	)
	ctx := context.Background()

	fn, err := lambdaMock.CreateFunction(ctx, serverlessdriver.FunctionConfig{
		Name: "my-func", Runtime: "go1.x", Handler: "main",
	})
	require.NoError(t, err)

	q, err := sqsMock.CreateQueue(ctx, mqdriver.QueueConfig{Name: "my-queue"})
	require.NoError(t, err)

	esm, err := lambdaMock.CreateEventSourceMapping(ctx, serverlessdriver.EventSourceMappingConfig{
		EventSourceArn: q.ARN, FunctionName: fn.Name, BatchSize: 10, Enabled: true,
	})
	require.NoError(t, err)

	graph, err := engine.BuildDependencyGraph(ctx)
	require.NoError(t, err)

	ids := resourceIDs(graph)
	assert.Contains(t, ids, fn.ARN)
	assert.Contains(t, ids, q.ARN)
	assert.Contains(t, ids, esm.UUID)

	// ESM triggers the function.
	assert.True(t, hasDependency(graph, esm.UUID, fn.ARN, "triggers"))
	// ESM is triggered by the queue.
	assert.True(t, hasDependency(graph, esm.UUID, q.ARN, "triggered-by"))
}

func TestAlarmNotificationGraph(t *testing.T) {
	opts := newTestOpts()
	ec2Mock := ec2.New(opts)
	vpcMock := vpc.New(opts)
	dnsMock := route53.New(opts)
	cwMock := cloudwatch.New(opts)
	engine := New(ec2Mock, vpcMock, dnsMock, WithMonitoring(cwMock))
	ctx := context.Background()

	ch, err := cwMock.CreateNotificationChannel(ctx, monitoringdriver.NotificationChannelConfig{
		Name: "ops-channel", Type: "email", Endpoint: "ops@example.com",
	})
	require.NoError(t, err)

	err = cwMock.CreateAlarm(ctx, monitoringdriver.AlarmConfig{
		Name: "high-cpu", Namespace: "AWS/EC2", MetricName: "CPUUtilization",
		ComparisonOperator: "GreaterThanThreshold", Threshold: 80,
		Period: 300, EvaluationPeriods: 1, Stat: "Average",
		AlarmActions: []string{ch.ID},
	})
	require.NoError(t, err)

	graph, err := engine.BuildDependencyGraph(ctx)
	require.NoError(t, err)

	assert.True(t, hasResourceType(graph, "alarm"))
	assert.True(t, hasResourceType(graph, "notification-channel"))
}

func TestInstanceProfileGraph(t *testing.T) {
	opts := newTestOpts()
	ec2Mock := ec2.New(opts)
	vpcMock := vpc.New(opts)
	dnsMock := route53.New(opts)
	iamMock := awsiam.New(opts)
	engine := New(ec2Mock, vpcMock, dnsMock, WithIAM(iamMock))
	ctx := context.Background()

	_, err := iamMock.CreateRole(ctx, iamdriver.RoleConfig{Name: "test-role"})
	require.NoError(t, err)

	profile, err := iamMock.CreateInstanceProfile(ctx, iamdriver.InstanceProfileConfig{
		Name: "test-profile", RoleName: "test-role",
	})
	require.NoError(t, err)

	graph, err := engine.BuildDependencyGraph(ctx)
	require.NoError(t, err)

	assert.Contains(t, resourceIDs(graph), profile.Name)
	assert.True(t, hasDependency(graph, profile.Name, "test-role", "uses"))
}

func TestWhatIfDelete(t *testing.T) {
	engine, ec2Mock, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	vpcID, _, _, _ := createGraphResources(t, ctx, ec2Mock, vpcMock)

	report, err := engine.WhatIf(ctx, "delete", vpcID)
	require.NoError(t, err)
	assert.Equal(t, "delete", report.Action)
	assert.NotEmpty(t, report.DirectlyAffected)
}

func TestWhatIfStop(t *testing.T) {
	engine, ec2Mock, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	_, _, _, instanceID := createGraphResources(t, ctx, ec2Mock, vpcMock)

	report, err := engine.WhatIf(ctx, "stop", instanceID)
	require.NoError(t, err)
	assert.Equal(t, "stop", report.Action)
	assert.Equal(t, instanceID, report.Target.ID)
	assert.NotEmpty(t, report.BrokenConnections)
	assert.Contains(t, report.Summary, "stopping")
}

func TestWhatIfDisconnect(t *testing.T) {
	engine, _, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	v1, err := vpcMock.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	v2, err := vpcMock.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.1.0.0/16"})
	require.NoError(t, err)

	peering, err := vpcMock.CreatePeeringConnection(ctx, netdriver.PeeringConfig{
		RequesterVPC: v1.ID, AccepterVPC: v2.ID,
	})
	require.NoError(t, err)

	err = vpcMock.AcceptPeeringConnection(ctx, peering.ID)
	require.NoError(t, err)

	report, err := engine.WhatIf(ctx, "disconnect", peering.ID)
	require.NoError(t, err)
	assert.Equal(t, "disconnect", report.Action)
	assert.NotEmpty(t, report.BrokenConnections)
	assert.Contains(t, report.Summary, "disconnecting")
}

func TestWhatIfInvalidAction(t *testing.T) {
	engine, _, _, _ := newTestEngine()
	ctx := context.Background()

	_, err := engine.WhatIf(ctx, "explode", "fake-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported action")
}

func TestOrphanedResources(t *testing.T) {
	engine, ec2Mock, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	vpcID, subnetID, _, _ := createGraphResources(t, ctx, ec2Mock, vpcMock)

	// Create a second subnet in the same VPC.
	subnet2, err := vpcMock.CreateSubnet(ctx, netdriver.SubnetConfig{
		VPCID: vpcID, CIDRBlock: "10.0.2.0/24", AvailabilityZone: "us-east-1b",
	})
	require.NoError(t, err)

	_ = subnetID

	// Deleting the VPC should orphan nothing (subnets are direct dependents).
	// But let's check that blast radius includes orphaned info.
	report, err := engine.BlastRadius(ctx, vpcID)
	require.NoError(t, err)
	assert.NotNil(t, report)

	// Both subnets should be direct dependents.
	directIDs := refIDs(report.DirectlyAffected)
	assert.Contains(t, directIDs, subnetID)
	assert.Contains(t, directIDs, subnet2.ID)
}

func TestProviderField(t *testing.T) {
	opts := newTestOpts()
	ec2Mock := ec2.New(opts)
	vpcMock := vpc.New(opts)
	dnsMock := route53.New(opts)
	engine := New(ec2Mock, vpcMock, dnsMock, WithProvider("aws"))
	ctx := context.Background()

	_, err := vpcMock.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	graph, err := engine.BuildDependencyGraph(ctx)
	require.NoError(t, err)

	for _, r := range graph.Resources {
		assert.Equal(t, "aws", r.Provider)
	}
}

func TestGetDependencyGraph(t *testing.T) {
	engine, ec2Mock, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	createGraphResources(t, ctx, ec2Mock, vpcMock)

	graph, err := engine.GetDependencyGraph(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, graph.Resources)
}

func TestEngineExportDOT(t *testing.T) {
	engine, ec2Mock, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	createGraphResources(t, ctx, ec2Mock, vpcMock)

	dot, err := engine.ExportDOT(ctx)
	require.NoError(t, err)
	assert.Contains(t, dot, "digraph CloudEmu")
}

func TestEngineExportMermaid(t *testing.T) {
	engine, ec2Mock, vpcMock, _ := newTestEngine()
	ctx := context.Background()

	createGraphResources(t, ctx, ec2Mock, vpcMock)

	mermaid, err := engine.ExportMermaid(ctx)
	require.NoError(t, err)
	assert.Contains(t, mermaid, "graph TD")
}
