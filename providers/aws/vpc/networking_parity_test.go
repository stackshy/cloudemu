package vpc

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func newMock() *Mock { return New(config.NewOptions()) }

func mustVPC(t *testing.T, m *Mock) (vpcID, subnetID string) {
	t.Helper()

	ctx := context.Background()

	v, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	if err != nil {
		t.Fatalf("CreateVPC: %v", err)
	}

	s, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: v.ID, CIDRBlock: "10.0.1.0/24"})
	if err != nil {
		t.Fatalf("CreateSubnet: %v", err)
	}

	return v.ID, s.ID
}

func TestTransitGatewayLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	vpcID, subnetID := mustVPC(t, m)

	tgw, err := m.CreateTransitGateway(ctx, driver.TransitGatewayConfig{Description: "hub"})
	if err != nil || tgw.ASN != defaultAmazonSideASN {
		t.Fatalf("CreateTransitGateway: %v %+v", err, tgw)
	}

	att, err := m.CreateTransitGatewayVPCAttachment(ctx, driver.TransitGatewayVPCAttachmentConfig{
		TransitGatewayID: tgw.ID, VPCID: vpcID, SubnetIDs: []string{subnetID},
	})
	if err != nil || att.VPCID != vpcID {
		t.Fatalf("CreateTransitGatewayVPCAttachment: %v %+v", err, att)
	}

	// Attachment to a missing transit gateway is rejected.
	if _, err := m.CreateTransitGatewayVPCAttachment(ctx, driver.TransitGatewayVPCAttachmentConfig{
		TransitGatewayID: "tgw-nope", VPCID: vpcID,
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("attach to missing tgw: got %v, want InvalidArgument", err)
	}

	rt, err := m.CreateTransitGatewayRouteTable(ctx, tgw.ID, nil)
	if err != nil {
		t.Fatalf("CreateTransitGatewayRouteTable: %v", err)
	}

	if _, err := m.DeleteTransitGatewayRouteTable(ctx, rt.ID); err != nil {
		t.Fatalf("DeleteTransitGatewayRouteTable: %v", err)
	}

	if _, err := m.DeleteTransitGateway(ctx, tgw.ID); err != nil {
		t.Fatalf("DeleteTransitGateway: %v", err)
	}

	if got, _ := m.DescribeTransitGateways(ctx, nil); len(got) != 0 {
		t.Fatalf("transit gateway survived delete: %+v", got)
	}
}

func TestVPNLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	vpcID, _ := mustVPC(t, m)

	if _, err := m.CreateCustomerGateway(ctx, driver.CustomerGatewayConfig{}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("customer gateway without IP: got %v, want InvalidArgument", err)
	}

	cgw, err := m.CreateCustomerGateway(ctx, driver.CustomerGatewayConfig{IPAddress: "203.0.113.10", BGPASN: 65000})
	if err != nil || cgw.Type != "ipsec.1" {
		t.Fatalf("CreateCustomerGateway: %v %+v", err, cgw)
	}

	vgw, err := m.CreateVPNGateway(ctx, driver.VPNGatewayConfig{})
	if err != nil {
		t.Fatalf("CreateVPNGateway: %v", err)
	}

	if _, err := m.AttachVPNGateway(ctx, vgw.ID, vpcID); err != nil {
		t.Fatalf("AttachVPNGateway: %v", err)
	}

	got, _ := m.DescribeVPNGateways(ctx, []string{vgw.ID})
	if len(got) != 1 || got[0].AttachedVPCID != vpcID {
		t.Fatalf("attached vgw wrong: %+v", got)
	}

	vpn, err := m.CreateVPNConnection(ctx, driver.VPNConnectionConfig{CustomerGatewayID: cgw.ID, VPNGatewayID: vgw.ID})
	if err != nil {
		t.Fatalf("CreateVPNConnection: %v", err)
	}

	// A connection with neither gateway is rejected.
	if _, err := m.CreateVPNConnection(ctx, driver.VPNConnectionConfig{CustomerGatewayID: cgw.ID}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("vpn connection without gateway: got %v, want InvalidArgument", err)
	}

	if err := m.DetachVPNGateway(ctx, vgw.ID, vpcID); err != nil {
		t.Fatalf("DetachVPNGateway: %v", err)
	}

	if err := m.DeleteVPNConnection(ctx, vpn.ID); err != nil {
		t.Fatalf("DeleteVPNConnection: %v", err)
	}
}

func TestDHCPPrefixEgressEndpointClientVPN(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	vpcID, subnetID := mustVPC(t, m)

	// DHCP options.
	opt, err := m.CreateDHCPOptions(ctx, driver.DHCPOptionsConfig{
		Configuration: map[string][]string{"domain-name-servers": {"10.0.0.2"}},
	})
	if err != nil {
		t.Fatalf("CreateDHCPOptions: %v", err)
	}

	if err := m.AssociateDHCPOptions(ctx, opt.ID, vpcID); err != nil {
		t.Fatalf("AssociateDHCPOptions: %v", err)
	}

	if err := m.AssociateDHCPOptions(ctx, "default", vpcID); err != nil {
		t.Fatalf("AssociateDHCPOptions default: %v", err)
	}

	// Prefix list.
	if _, err := m.CreateManagedPrefixList(ctx, driver.PrefixListConfig{Name: "x"}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("prefix list without maxEntries: got %v, want InvalidArgument", err)
	}

	pl, err := m.CreateManagedPrefixList(ctx, driver.PrefixListConfig{
		Name: "corp", MaxEntries: 10, Entries: []driver.PrefixListEntry{{CIDR: "10.0.0.0/8"}},
	})
	if err != nil {
		t.Fatalf("CreateManagedPrefixList: %v", err)
	}

	entries, _ := m.GetManagedPrefixListEntries(ctx, pl.ID)
	if len(entries) != 1 {
		t.Fatalf("prefix list entries: %+v", entries)
	}

	// Egress-only IGW.
	if _, err := m.CreateEgressOnlyInternetGateway(ctx, "vpc-nope", nil); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("egress-only under missing vpc: got %v, want InvalidArgument", err)
	}

	eigw, err := m.CreateEgressOnlyInternetGateway(ctx, vpcID, nil)
	if err != nil || eigw.AttachedVPCID != vpcID {
		t.Fatalf("CreateEgressOnlyInternetGateway: %v %+v", err, eigw)
	}

	// Endpoint service.
	if _, err := m.CreateVPCEndpointServiceConfiguration(ctx, driver.EndpointServiceConfig{}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("endpoint service without NLB: got %v, want InvalidArgument", err)
	}

	svc, err := m.CreateVPCEndpointServiceConfiguration(ctx, driver.EndpointServiceConfig{
		NetworkLoadBalancerARNs: []string{"arn:aws:elasticloadbalancing:us-east-1:0:loadbalancer/net/x/1"},
	})
	if err != nil || svc.ServiceName == "" {
		t.Fatalf("CreateVPCEndpointServiceConfiguration: %v %+v", err, svc)
	}

	// Client VPN.
	ep, err := m.CreateClientVPNEndpoint(ctx, driver.ClientVPNEndpointConfig{
		ClientCIDRBlock: "10.100.0.0/16", ServerCertificateARN: "arn:aws:acm:us-east-1:0:certificate/abc",
	})
	if err != nil || ep.State != "pending-associate" {
		t.Fatalf("CreateClientVPNEndpoint: %v %+v", err, ep)
	}

	assoc, err := m.AssociateClientVPNTargetNetwork(ctx, ep.ID, subnetID)
	if err != nil {
		t.Fatalf("AssociateClientVPNTargetNetwork: %v", err)
	}

	got, _ := m.DescribeClientVPNEndpoints(ctx, []string{ep.ID})
	if len(got) != 1 || got[0].State != "available" {
		t.Fatalf("client vpn not available after associate: %+v", got)
	}

	if err := m.DisassociateClientVPNTargetNetwork(ctx, ep.ID, assoc.AssociationID); err != nil {
		t.Fatalf("DisassociateClientVPNTargetNetwork: %v", err)
	}
}
