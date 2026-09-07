package topology

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	ocicompute "github.com/stackshy/cloudemu/v2/providers/oci/compute"
	ocivcn "github.com/stackshy/cloudemu/v2/providers/oci/vcn"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// newOCIEngine builds a topology engine over the OCI mocks, cross-wired the
// way providers/oci.New wires them.
func newOCIEngine(t *testing.T) (*Engine, *ocicompute.Mock, *ocivcn.Mock) {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-ashburn-1"))

	vcnMock := ocivcn.New(opts)
	computeMock := ocicompute.New(opts)
	computeMock.SetNetworking(vcnMock)

	return New(computeMock, vcnMock, nil), computeMock, vcnMock
}

// TestCanConnectHonoursOCISecurityLists pins the gap deferred from the VCN
// review: an OCI instance is governed by its subnet's security lists as well
// as its VNIC's network security groups, and a pair allowed only by a security
// list must read as reachable rather than denied for want of an NSG.
func TestCanConnectHonoursOCISecurityLists(t *testing.T) {
	engine, computeMock, vcnMock := newOCIEngine(t)
	ctx := t.Context()

	vcn, err := vcnMock.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	subnet, err := vcnMock.CreateSubnet(ctx, netdriver.SubnetConfig{
		VPCID:     vcn.ID,
		CIDRBlock: "10.0.1.0/24",
	})
	require.NoError(t, err)

	launched, err := computeMock.RunInstances(ctx, computedriver.InstanceConfig{
		InstanceType: "VM.Standard.E4.Flex",
		SubnetID:     subnet.ID,
	}, 2)
	require.NoError(t, err)
	require.Len(t, launched, 2)

	src, dst := launched[0], launched[1]

	// No NSG was named at launch, so the only rules governing the pair are the
	// VCN's default security list: inbound SSH and unrestricted egress.
	assert.Empty(t, vcnMock.Defaults(vcn.ID).SecurityListID == "", "the VCN has a default security list")
	assert.Contains(t, src.SecurityGroups, vcnMock.Defaults(vcn.ID).SecurityListID,
		"the subnet's security list governs the instance")

	tests := []struct {
		name        string
		port        int
		protocol    string
		wantAllowed bool
	}{
		{name: "ssh is allowed by the default security list", port: 22, protocol: "tcp", wantAllowed: true},
		{name: "http has no security list rule", port: 80, protocol: "tcp", wantAllowed: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := engine.CanConnect(ctx, ConnectivityQuery{
				SrcInstanceID: src.ID,
				DstInstanceID: dst.ID,
				Port:          tc.port,
				Protocol:      tc.protocol,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.wantAllowed, result.Allowed, result.Reason)
		})
	}
}

// TestCanConnectHonoursOCINetworkSecurityGroups keeps the NSG path working
// alongside the security list one: the two are a union, so a rule in either
// allows the traffic.
func TestCanConnectHonoursOCINetworkSecurityGroups(t *testing.T) {
	engine, computeMock, vcnMock := newOCIEngine(t)
	ctx := t.Context()

	vcn, err := vcnMock.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	subnet, err := vcnMock.CreateSubnet(ctx, netdriver.SubnetConfig{
		VPCID:     vcn.ID,
		CIDRBlock: "10.0.1.0/24",
	})
	require.NoError(t, err)

	nsg, err := vcnMock.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{
		VPCID: vcn.ID,
		Name:  "web",
	})
	require.NoError(t, err)

	require.NoError(t, vcnMock.AddIngressRule(ctx, nsg.ID, netdriver.SecurityRule{
		Protocol: "tcp", CIDR: "10.0.0.0/16", FromPort: 80, ToPort: 80,
	}))

	launched, err := computeMock.RunInstances(ctx, computedriver.InstanceConfig{
		InstanceType:   "VM.Standard.E4.Flex",
		SubnetID:       subnet.ID,
		SecurityGroups: []string{nsg.ID},
	}, 2)
	require.NoError(t, err)

	result, err := engine.CanConnect(ctx, ConnectivityQuery{
		SrcInstanceID: launched[0].ID,
		DstInstanceID: launched[1].ID,
		Port:          80,
		Protocol:      "tcp",
	})
	require.NoError(t, err)
	assert.True(t, result.Allowed, result.Reason)
	require.NotNil(t, result.SGVerdict.IngressMatch)
	assert.Equal(t, nsg.ID, result.SGVerdict.IngressMatch.GroupID,
		"the NSG rule is what allowed the traffic")
}
