package vcn_test

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/oci/vcn"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

const (
	testCompartment  = "ocid1.compartment.oc1..aaaaaaaatest"
	otherCompartment = "ocid1.compartment.oc1..aaaaaaaaother"
	vcnCIDR          = "10.0.0.0/16"
	subnetCIDR       = "10.0.1.0/24"
)

func newMock(t *testing.T) *vcn.Mock {
	t.Helper()

	return vcn.New(config.NewOptions(
		config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(testCompartment),
	))
}

// newVCN creates a VCN and fails the test if the driver refuses.
func newVCN(t *testing.T, m *vcn.Mock, cidr string) *driver.VPCInfo {
	t.Helper()

	info, err := m.CreateVPC(context.Background(), driver.VPCConfig{CIDRBlock: cidr})
	require.NoError(t, err)

	return info
}

func TestCreateVPCValidatesCIDR(t *testing.T) {
	tests := []struct {
		name string
		cidr string
		code cerrors.Code
	}{
		{name: "valid block", cidr: vcnCIDR, code: cerrors.OK},
		{name: "empty block", cidr: "", code: cerrors.InvalidArgument},
		{name: "not a CIDR", cidr: "10.0.0.0", code: cerrors.InvalidArgument},
		{name: "garbage", cidr: "not-a-cidr", code: cerrors.InvalidArgument},
		{name: "bad mask", cidr: "10.0.0.0/64", code: cerrors.InvalidArgument},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newMock(t)

			info, err := m.CreateVPC(context.Background(), driver.VPCConfig{CIDRBlock: tc.cidr})
			if tc.code == cerrors.OK {
				require.NoError(t, err)
				assert.Equal(t, tc.cidr, info.CIDRBlock)

				return
			}

			require.Error(t, err)
			assert.Equal(t, tc.code, cerrors.GetCode(err))
		})
	}
}

func TestCreateVPCCreatesDefaultResources(t *testing.T) {
	m := newMock(t)
	info := newVCN(t, m, vcnCIDR)

	defaults := m.Defaults(info.ID)
	assert.NotEmpty(t, defaults.RouteTableID)
	assert.NotEmpty(t, defaults.SecurityListID)
	assert.NotEmpty(t, defaults.DHCPOptionsID)

	tables, err := m.DescribeRouteTables(context.Background(), []string{defaults.RouteTableID})
	require.NoError(t, err)
	require.Len(t, tables, 1)
	assert.Equal(t, vcnCIDR, tables[0].Routes[0].DestinationCIDR, "the default table carries the local route")

	acls, err := m.DescribeNetworkACLs(context.Background(), []string{defaults.SecurityListID})
	require.NoError(t, err)
	require.Len(t, acls, 1)
	assert.True(t, acls[0].IsDefault)
}

func TestVPCLifecycle(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	info := newVCN(t, m, vcnCIDR)

	found, err := m.DescribeVPCs(ctx, []string{info.ID})
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, vcn.StateAvailable, found[0].State)

	require.NoError(t, m.DeleteVPC(ctx, info.ID))

	err = m.DeleteVPC(ctx, info.ID)
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestDeleteVPCRefusesWhileOccupied(t *testing.T) {
	tests := []struct {
		name   string
		occupy func(t *testing.T, m *vcn.Mock, vcnID string)
	}{
		{
			name: "subnet remains",
			occupy: func(t *testing.T, m *vcn.Mock, vcnID string) {
				t.Helper()
				_, err := m.CreateSubnet(context.Background(),
					driver.SubnetConfig{VPCID: vcnID, CIDRBlock: subnetCIDR})
				require.NoError(t, err)
			},
		},
		{
			name: "internet gateway remains",
			occupy: func(t *testing.T, m *vcn.Mock, vcnID string) {
				t.Helper()
				igw, err := m.CreateInternetGateway(context.Background(), driver.InternetGatewayConfig{})
				require.NoError(t, err)
				require.NoError(t, m.AttachInternetGateway(context.Background(), igw.ID, vcnID))
			},
		},
		{
			name: "NAT gateway remains",
			occupy: func(t *testing.T, m *vcn.Mock, vcnID string) {
				t.Helper()
				_, err := m.CreateNATGateway(context.Background(), driver.NATGatewayConfig{SubnetID: vcnID})
				require.NoError(t, err)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newMock(t)
			info := newVCN(t, m, vcnCIDR)
			tc.occupy(t, m, info.ID)

			err := m.DeleteVPC(context.Background(), info.ID)
			require.Error(t, err)
			assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))
		})
	}
}

func TestCreateSubnetValidatesCIDR(t *testing.T) {
	tests := []struct {
		name     string
		existing string
		cidr     string
		code     cerrors.Code
	}{
		{name: "inside the VCN", cidr: subnetCIDR, code: cerrors.OK},
		{name: "outside the VCN", cidr: "192.168.0.0/24", code: cerrors.InvalidArgument},
		{name: "wider than the VCN", cidr: "10.0.0.0/8", code: cerrors.InvalidArgument},
		{name: "malformed", cidr: "10.0.1.0", code: cerrors.InvalidArgument},
		{name: "empty", cidr: "", code: cerrors.InvalidArgument},
		{name: "overlaps a sibling", existing: subnetCIDR, cidr: "10.0.1.128/25", code: cerrors.InvalidArgument},
		{name: "beside a sibling", existing: subnetCIDR, cidr: "10.0.2.0/24", code: cerrors.OK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newMock(t)
			ctx := context.Background()
			parent := newVCN(t, m, vcnCIDR)

			if tc.existing != "" {
				_, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: parent.ID, CIDRBlock: tc.existing})
				require.NoError(t, err)
			}

			info, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: parent.ID, CIDRBlock: tc.cidr})
			if tc.code == cerrors.OK {
				require.NoError(t, err)
				assert.Equal(t, parent.ID, info.VPCID)

				return
			}

			require.Error(t, err)
			assert.Equal(t, tc.code, cerrors.GetCode(err))
		})
	}
}

func TestCreateSubnetRequiresKnownVCN(t *testing.T) {
	m := newMock(t)

	_, err := m.CreateSubnet(context.Background(),
		driver.SubnetConfig{VPCID: "ocid1.vcn.oc1.iad.missing", CIDRBlock: subnetCIDR})
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestOCIDShapePerResourceType(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	parent := newVCN(t, m, vcnCIDR)
	defaults := m.Defaults(parent.ID)

	subnet, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: parent.ID, CIDRBlock: subnetCIDR})
	require.NoError(t, err)

	nsg, err := m.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{Name: "nsg", VPCID: parent.ID})
	require.NoError(t, err)

	igw, err := m.CreateInternetGateway(ctx, driver.InternetGatewayConfig{})
	require.NoError(t, err)

	nat, err := m.CreateNATGateway(ctx, driver.NATGatewayConfig{SubnetID: subnet.ID})
	require.NoError(t, err)

	svc, err := m.CreateVPCEndpoint(ctx, driver.VPCEndpointConfig{VPCID: parent.ID, ServiceName: "ocid1.service.oc1..oss"})
	require.NoError(t, err)

	ip, err := m.AllocateAddress(ctx, driver.ElasticIPConfig{})
	require.NoError(t, err)

	vnic, err := m.CreateNetworkInterface(ctx, subnet.ID, "primary", nil)
	require.NoError(t, err)

	privateIPs, err := m.DescribePrivateIPs(ctx, nil)
	require.NoError(t, err)
	require.Len(t, privateIPs, 1)

	tests := []struct {
		name         string
		id           string
		resourceType string
	}{
		{name: "vcn", id: parent.ID, resourceType: "vcn"},
		{name: "subnet", id: subnet.ID, resourceType: "subnet"},
		{name: "network security group", id: nsg.ID, resourceType: "networksecuritygroup"},
		{name: "security list", id: defaults.SecurityListID, resourceType: "securitylist"},
		{name: "route table", id: defaults.RouteTableID, resourceType: "routetable"},
		{name: "dhcp options", id: defaults.DHCPOptionsID, resourceType: "dhcpoptions"},
		{name: "internet gateway", id: igw.ID, resourceType: "internetgateway"},
		{name: "nat gateway", id: nat.ID, resourceType: "natgateway"},
		{name: "service gateway", id: svc.ID, resourceType: "servicegateway"},
		{name: "public ip", id: ip.AllocationID, resourceType: "publicip"},
		{name: "vnic", id: vnic.ID, resourceType: "vnic"},
		{name: "private ip", id: privateIPs[0].ID, resourceType: "privateip"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := regexp.MustCompile(`^ocid1\.` + tc.resourceType + `\.oc1\.iad\.[a-z0-9]+$`)
			assert.Regexp(t, want, tc.id)
		})
	}
}

func TestCompartmentScope(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	here := newVCN(t, m, vcnCIDR)
	elsewhere := newVCN(t, m, "172.16.0.0/16")
	m.SetScope(elsewhere.ID, scope.Scope{Compartment: otherCompartment})

	assert.Equal(t, testCompartment, m.Scope(here.ID).Compartment, "creates land in the default compartment")
	assert.Equal(t, otherCompartment, m.Scope(elsewhere.ID).Compartment)

	filter := scope.Scope{Compartment: testCompartment}
	assert.True(t, m.Scope(here.ID).Matches(filter))
	assert.False(t, m.Scope(elsewhere.ID).Matches(filter), "a VCN in another compartment must not list")

	require.NoError(t, m.DeleteVPC(ctx, elsewhere.ID))
	assert.True(t, m.Scope(elsewhere.ID).IsZero(), "deleting forgets the compartment")
	assert.Empty(t, m.Created(elsewhere.ID))
}

func TestCreatedIsRecorded(t *testing.T) {
	clock := config.NewFakeClock(mustTime(t))
	m := vcn.New(config.NewOptions(config.WithClock(clock), config.WithRegion("eu-frankfurt-1")))

	info := newVCN(t, m, vcnCIDR)

	assert.Equal(t, "2026-01-02T03:04:05Z", m.Created(info.ID))
	assert.Empty(t, m.Created("ocid1.vcn.oc1.fra.unknown"))
}

func TestSecurityGroupRules(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	parent := newVCN(t, m, vcnCIDR)

	nsg, err := m.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{Name: "web", VPCID: parent.ID})
	require.NoError(t, err)
	assert.Empty(t, nsg.IngressRules, "an OCI NSG starts empty")

	rule := driver.SecurityRule{Protocol: "tcp", FromPort: 443, ToPort: 443, CIDR: "0.0.0.0/0"}
	require.NoError(t, m.AddIngressRule(ctx, nsg.ID, rule))
	require.NoError(t, m.AddEgressRule(ctx, nsg.ID, rule))

	got, err := m.DescribeSecurityGroups(ctx, []string{nsg.ID})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Len(t, got[0].IngressRules, 1)
	assert.Len(t, got[0].EgressRules, 1)

	require.NoError(t, m.RemoveIngressRule(ctx, nsg.ID, rule))

	err = m.RemoveIngressRule(ctx, nsg.ID, rule)
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	err = m.AddIngressRule(ctx, "ocid1.networksecuritygroup.oc1.iad.missing", rule)
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestSecurityGroupDeleteRefusesWithMembers(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	parent := newVCN(t, m, vcnCIDR)

	subnet, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: parent.ID, CIDRBlock: subnetCIDR})
	require.NoError(t, err)

	nsg, err := m.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{Name: "web", VPCID: parent.ID})
	require.NoError(t, err)

	vnic, err := m.CreateNetworkInterface(ctx, subnet.ID, "primary", nil)
	require.NoError(t, err)

	_, err = m.UpdateVNIC(ctx, vnic.ID, nil, nil, []string{nsg.ID})
	require.NoError(t, err)

	err = m.DeleteSecurityGroup(ctx, nsg.ID)
	require.Error(t, err)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	members, err := m.VNICsInNSG(ctx, nsg.ID)
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.Equal(t, vnic.ID, members[0].ID)
}

func TestSecurityListRules(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	parent := newVCN(t, m, vcnCIDR)

	list, err := m.CreateNetworkACL(ctx, parent.ID, map[string]string{"env": "dev"})
	require.NoError(t, err)
	assert.False(t, list.IsDefault)
	assert.Len(t, list.Rules, 2, "OCI seeds a new security list with SSH in and everything out")

	require.NoError(t, m.AddNetworkACLRule(ctx, list.ID,
		&driver.NetworkACLRule{RuleNumber: 2, Protocol: "tcp", CIDR: "10.0.0.0/16", FromPort: 80, ToPort: 80}))

	err = m.AddNetworkACLRule(ctx, list.ID, &driver.NetworkACLRule{RuleNumber: 3, Action: "deny", CIDR: "0.0.0.0/0"})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err), "security lists have no deny rules")

	require.NoError(t, m.RemoveNetworkACLRule(ctx, list.ID, 2, false))
	require.NoError(t, m.DeleteNetworkACL(ctx, list.ID))

	err = m.DeleteNetworkACL(ctx, m.Defaults(parent.ID).SecurityListID)
	require.Error(t, err)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err), "the default list dies with the VCN")
}

func TestRouteTablesAndAssociations(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	parent := newVCN(t, m, vcnCIDR)

	subnet, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: parent.ID, CIDRBlock: subnetCIDR})
	require.NoError(t, err)

	table, err := m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: parent.ID})
	require.NoError(t, err)

	igw, err := m.CreateInternetGateway(ctx, driver.InternetGatewayConfig{})
	require.NoError(t, err)

	require.NoError(t, m.CreateRoute(ctx, table.ID, "0.0.0.0/0", igw.ID, ""))

	err = m.CreateRoute(ctx, table.ID, "0.0.0.0/0", igw.ID, "")
	require.Error(t, err)
	assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))

	tables, err := m.DescribeRouteTables(ctx, []string{table.ID})
	require.NoError(t, err)
	require.Len(t, tables, 1)
	require.Len(t, tables[0].Routes, 2)
	assert.Equal(t, "gateway", tables[0].Routes[1].TargetType, "the target kind comes off the OCID")

	assoc, err := m.AssociateRouteTable(ctx, table.ID, subnet.ID)
	require.NoError(t, err)

	err = m.DeleteRouteTable(ctx, table.ID)
	require.Error(t, err)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	require.NoError(t, m.DisassociateRouteTable(ctx, assoc.ID))
	require.NoError(t, m.DeleteRouteTable(ctx, table.ID))

	err = m.DeleteRouteTable(ctx, m.Defaults(parent.ID).RouteTableID)
	require.Error(t, err)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))
}

func TestSubnetTakesOneRouteTable(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	parent := newVCN(t, m, vcnCIDR)

	subnet, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: parent.ID, CIDRBlock: subnetCIDR})
	require.NoError(t, err)

	first, err := m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: parent.ID})
	require.NoError(t, err)

	second, err := m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: parent.ID})
	require.NoError(t, err)

	_, err = m.AssociateRouteTable(ctx, first.ID, subnet.ID)
	require.NoError(t, err)

	_, err = m.AssociateRouteTable(ctx, second.ID, subnet.ID)
	require.NoError(t, err)

	tables, err := m.DescribeRouteTables(ctx, []string{first.ID, second.ID})
	require.NoError(t, err)
	require.Len(t, tables, 2)
	assert.Empty(t, tables[0].Associations, "the first attachment is replaced")
	assert.Len(t, tables[1].Associations, 1)
}

func TestNATGatewayAcceptsVCNOrSubnet(t *testing.T) {
	tests := []struct {
		name    string
		useVCN  bool
		wantSub bool
	}{
		{name: "named by VCN", useVCN: true},
		{name: "named by subnet", wantSub: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newMock(t)
			ctx := context.Background()
			parent := newVCN(t, m, vcnCIDR)

			subnet, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: parent.ID, CIDRBlock: subnetCIDR})
			require.NoError(t, err)

			target := subnet.ID
			if tc.useVCN {
				target = parent.ID
			}

			nat, err := m.CreateNATGateway(ctx, driver.NATGatewayConfig{SubnetID: target})
			require.NoError(t, err)
			assert.Equal(t, parent.ID, nat.VPCID)
			assert.NotEmpty(t, nat.PublicIP)

			if tc.wantSub {
				assert.Equal(t, subnet.ID, nat.SubnetID)
			} else {
				assert.Empty(t, nat.SubnetID)
			}
		})
	}
}

func TestNATGatewayRejectsUnknownTarget(t *testing.T) {
	m := newMock(t)

	_, err := m.CreateNATGateway(context.Background(), driver.NATGatewayConfig{SubnetID: "ocid1.vcn.oc1.iad.missing"})
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestInternetGatewayAttachment(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	parent := newVCN(t, m, vcnCIDR)

	igw, err := m.CreateInternetGateway(ctx, driver.InternetGatewayConfig{})
	require.NoError(t, err)
	assert.Equal(t, vcn.StateDetached, igw.State)

	require.NoError(t, m.AttachInternetGateway(ctx, igw.ID, parent.ID))

	err = m.AttachInternetGateway(ctx, igw.ID, parent.ID)
	require.Error(t, err)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	err = m.DeleteInternetGateway(ctx, igw.ID)
	require.Error(t, err)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	require.NoError(t, m.DetachInternetGateway(ctx, igw.ID, parent.ID))
	require.NoError(t, m.DeleteInternetGateway(ctx, igw.ID))
}

func TestPublicIPAssignment(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	parent := newVCN(t, m, vcnCIDR)

	subnet, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: parent.ID, CIDRBlock: subnetCIDR})
	require.NoError(t, err)

	vnic, err := m.CreateNetworkInterface(ctx, subnet.ID, "primary", nil)
	require.NoError(t, err)

	privateIPs, err := m.DescribePrivateIPs(ctx, nil)
	require.NoError(t, err)
	require.Len(t, privateIPs, 1)
	assert.Equal(t, "10.0.1.2", privateIPs[0].Address, "OCI reserves the first two addresses")
	assert.True(t, privateIPs[0].IsPrimary)

	ip, err := m.AllocateAddress(ctx, driver.ElasticIPConfig{})
	require.NoError(t, err)
	assert.Equal(t, vcn.LifetimeReserved, ip.AllocationMethod)

	_, err = m.AssociateAddress(ctx, ip.AllocationID, privateIPs[0].ID)
	require.NoError(t, err)

	err = m.ReleaseAddress(ctx, ip.AllocationID)
	require.Error(t, err)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	vnics, err := m.DescribeVNICs(ctx, []string{vnic.ID})
	require.NoError(t, err)
	require.Len(t, vnics, 1)
	assert.Equal(t, ip.PublicIP, vnics[0].PublicIP)

	require.NoError(t, m.DisassociateAddress(ctx, privateIPs[0].ID))
	require.NoError(t, m.ReleaseAddress(ctx, ip.AllocationID))
}

func TestPrivateIPs(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	parent := newVCN(t, m, vcnCIDR)

	subnet, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: parent.ID, CIDRBlock: subnetCIDR})
	require.NoError(t, err)

	vnic, err := m.CreateNetworkInterface(ctx, subnet.ID, "primary", nil)
	require.NoError(t, err)

	secondary, err := m.CreatePrivateIP(ctx, vnic.ID, "", "second", "host2")
	require.NoError(t, err)
	assert.Equal(t, "10.0.1.3", secondary.Address)
	assert.False(t, secondary.IsPrimary)

	_, err = m.CreatePrivateIP(ctx, vnic.ID, secondary.Address, "clash", "")
	require.Error(t, err)
	assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))

	primary, err := m.DescribePrivateIPs(ctx, nil)
	require.NoError(t, err)
	require.Len(t, primary, 2)

	require.NoError(t, m.DeletePrivateIP(ctx, secondary.ID))

	err = m.DeleteSubnet(ctx, subnet.ID)
	require.Error(t, err)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err), "a subnet with VNICs cannot go")

	require.NoError(t, m.DeleteNetworkInterface(ctx, vnic.ID))
	require.NoError(t, m.DeleteSubnet(ctx, subnet.ID))
}

func TestDHCPOptions(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	parent := newVCN(t, m, vcnCIDR)

	defaults, err := m.DescribeDHCPOptions(ctx, []string{m.Defaults(parent.ID).DHCPOptionsID})
	require.NoError(t, err)
	require.Len(t, defaults, 1)
	assert.Equal(t, vcn.ServerTypeVCNLocalPlusInternet, defaults[0].ServerType)
	assert.True(t, defaults[0].IsDefault)

	custom, err := m.CreateDHCPOptions(ctx, parent.ID, "custom",
		vcn.ServerTypeCustomDNS, []string{"8.8.8.8"}, []string{"corp.example"})
	require.NoError(t, err)
	assert.Equal(t, []string{"8.8.8.8"}, custom.CustomDNSServer)

	_, err = m.CreateDHCPOptions(ctx, parent.ID, "bad", vcn.ServerTypeCustomDNS, nil, nil)
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	name := "renamed"
	updated, err := m.UpdateDHCPOptions(ctx, custom.ID, &name, "", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, name, updated.Name)

	require.NoError(t, m.DeleteDHCPOptions(ctx, custom.ID))

	err = m.DeleteDHCPOptions(ctx, defaults[0].ID)
	require.Error(t, err)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))
}

func TestServiceGateway(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	parent := newVCN(t, m, vcnCIDR)

	gw, err := m.CreateVPCEndpoint(ctx, driver.VPCEndpointConfig{
		VPCID:       parent.ID,
		ServiceName: "ocid1.service.oc1..objectstorage",
	})
	require.NoError(t, err)
	assert.Equal(t, vcn.EndpointTypeGateway, gw.EndpointType)

	_, err = m.CreateVPCEndpoint(ctx, driver.VPCEndpointConfig{VPCID: parent.ID})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	updated, err := m.ModifyVPCEndpoint(ctx, gw.ID, driver.VPCEndpointConfig{RouteTableIDs: []string{"rt"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"rt"}, updated.RouteTableIDs)

	require.NoError(t, m.DeleteVPCEndpoint(ctx, gw.ID))

	err = m.DeleteVPCEndpoint(ctx, gw.ID)
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestPeeringRefusesOverlappingCIDRs(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	first := newVCN(t, m, vcnCIDR)
	overlapping := newVCN(t, m, "10.0.0.0/24")
	distinct := newVCN(t, m, "172.16.0.0/16")

	_, err := m.CreatePeeringConnection(ctx,
		driver.PeeringConfig{RequesterVPC: first.ID, AccepterVPC: overlapping.ID})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	peering, err := m.CreatePeeringConnection(ctx,
		driver.PeeringConfig{RequesterVPC: first.ID, AccepterVPC: distinct.ID})
	require.NoError(t, err)
	assert.Equal(t, vcn.PeeringStatusPending, peering.Status)

	require.NoError(t, m.AcceptPeeringConnection(ctx, peering.ID))

	err = m.AcceptPeeringConnection(ctx, peering.ID)
	require.Error(t, err)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	require.NoError(t, m.DeletePeeringConnection(ctx, peering.ID))
}

func TestFlowLogs(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	parent := newVCN(t, m, vcnCIDR)

	log, err := m.CreateFlowLog(ctx, driver.FlowLogConfig{ResourceID: parent.ID, ResourceType: "VPC"})
	require.NoError(t, err)
	assert.Equal(t, vcn.TrafficTypeAll, log.TrafficType)

	_, err = m.CreateFlowLog(ctx, driver.FlowLogConfig{ResourceID: parent.ID, ResourceType: "Gateway"})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	records, err := m.GetFlowLogRecords(ctx, log.ID, 3)
	require.NoError(t, err)
	assert.Len(t, records, 3)

	require.NoError(t, m.DeleteFlowLog(ctx, log.ID))
}

func TestTagMutation(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	parent := newVCN(t, m, vcnCIDR)

	require.NoError(t, m.UpdateVPCTags(ctx, parent.ID, map[string]string{"env": "dev", "team": "net"}))
	require.NoError(t, m.RemoveVPCTags(ctx, parent.ID, []string{"team"}))

	got, err := m.DescribeVPCs(ctx, []string{parent.ID})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"env": "dev"}, got[0].Tags)

	require.NoError(t, m.SetTags(parent.ID, map[string]string{"only": "this"}))

	got, err = m.DescribeVPCs(ctx, []string{parent.ID})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"only": "this"}, got[0].Tags, "SetTags replaces rather than merges")

	err = m.SetTags("ocid1.vcn.oc1.iad.missing", nil)
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	err = m.UpdateVPCTags(ctx, "ocid1.vcn.oc1.iad.missing", nil)
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestModifyVPCAttribute(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	parent := newVCN(t, m, vcnCIDR)

	assert.True(t, parent.EnableDNSSupport, "a new VCN resolves its own records")
	assert.False(t, parent.EnableDNSHostnames)

	on := true
	require.NoError(t, m.ModifyVPCAttribute(ctx, parent.ID, driver.VPCAttributeUpdate{EnableDNSHostnames: &on}))

	got, err := m.DescribeVPCs(ctx, []string{parent.ID})
	require.NoError(t, err)
	assert.True(t, got[0].EnableDNSSupport, "the untouched attribute survives")
	assert.True(t, got[0].EnableDNSHostnames)

	err = m.ModifyVPCAttribute(ctx, "ocid1.vcn.oc1.iad.missing", driver.VPCAttributeUpdate{})
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestDescribeUnknownIDsReturnsEmpty(t *testing.T) {
	m := newMock(t)

	infos, err := m.DescribeVPCs(context.Background(), []string{"ocid1.vcn.oc1.iad.missing"})
	require.NoError(t, err)
	assert.Empty(t, infos, "Describe filters by id rather than failing")
}

// mustTime is the fixed instant the clock-driven tests run at.
func mustTime(t *testing.T) time.Time {
	t.Helper()

	ts, err := time.Parse(time.RFC3339, "2026-01-02T03:04:05Z")
	require.NoError(t, err)

	return ts
}
