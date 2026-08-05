package vpc

import (
	"context"
	"net"
	"sync"
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

	// Routing depth: associate, add a route, search.
	if _, err := m.AssociateTransitGatewayRouteTable(ctx, rt.ID, att.ID); err != nil {
		t.Fatalf("AssociateTransitGatewayRouteTable: %v", err)
	}

	if _, err := m.CreateTransitGatewayRoute(ctx, rt.ID, "10.9.0.0/16", att.ID); err != nil {
		t.Fatalf("CreateTransitGatewayRoute: %v", err)
	}

	routes, err := m.SearchTransitGatewayRoutes(ctx, rt.ID)
	if err != nil || len(routes) != 1 || routes[0].DestinationCIDR != "10.9.0.0/16" {
		t.Fatalf("SearchTransitGatewayRoutes: %v %+v", err, routes)
	}

	if err := m.EnableTransitGatewayRouteTablePropagation(ctx, rt.ID, att.ID); err != nil {
		t.Fatalf("EnableTransitGatewayRouteTablePropagation: %v", err)
	}

	if _, err := m.DeleteTransitGatewayRoute(ctx, rt.ID, "10.9.0.0/16"); err != nil {
		t.Fatalf("DeleteTransitGatewayRoute: %v", err)
	}

	// A route in a missing route table is rejected.
	if _, err := m.CreateTransitGatewayRoute(ctx, "tgw-rtb-nope", "10.0.0.0/8", att.ID); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("route in missing table: got %v, want InvalidArgument", err)
	}

	if _, err := m.DeleteTransitGatewayRouteTable(ctx, rt.ID); err != nil {
		t.Fatalf("DeleteTransitGatewayRouteTable: %v", err)
	}

	// A transit gateway with a live attachment cannot be deleted.
	if _, err := m.DeleteTransitGateway(ctx, tgw.ID); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("delete in-use tgw: got %v, want FailedPrecondition", err)
	}

	if _, err := m.DeleteTransitGatewayVPCAttachment(ctx, att.ID); err != nil {
		t.Fatalf("DeleteTransitGatewayVPCAttachment: %v", err)
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

	// Static route depth.
	if err := m.CreateVPNConnectionRoute(ctx, vpn.ID, "192.168.0.0/16"); err != nil {
		t.Fatalf("CreateVPNConnectionRoute: %v", err)
	}

	// Duplicate route is a no-op.
	if err := m.CreateVPNConnectionRoute(ctx, vpn.ID, "192.168.0.0/16"); err != nil {
		t.Fatalf("CreateVPNConnectionRoute dup: %v", err)
	}

	gotVPN, _ := m.DescribeVPNConnections(ctx, []string{vpn.ID})
	if len(gotVPN) != 1 || len(gotVPN[0].Routes) != 1 {
		t.Fatalf("vpn routes wrong: %+v", gotVPN)
	}

	// Re-target to a transit gateway clears the vpn gateway.
	tgw, err := m.CreateTransitGateway(ctx, driver.TransitGatewayConfig{})
	if err != nil {
		t.Fatalf("CreateTransitGateway: %v", err)
	}

	// Modify rejects a nonexistent gateway.
	if _, err := m.ModifyVPNConnection(ctx, vpn.ID, "tgw-nope", ""); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("modify to missing tgw: got %v, want InvalidArgument", err)
	}

	mod, err := m.ModifyVPNConnection(ctx, vpn.ID, tgw.ID, "")
	if err != nil || mod.TransitGatewayID != tgw.ID || mod.VPNGatewayID != "" {
		t.Fatalf("ModifyVPNConnection: %v %+v", err, mod)
	}

	if err := m.DeleteVPNConnectionRoute(ctx, vpn.ID, "192.168.0.0/16"); err != nil {
		t.Fatalf("DeleteVPNConnectionRoute: %v", err)
	}

	if err := m.DetachVPNGateway(ctx, vgw.ID, vpcID); err != nil {
		t.Fatalf("DetachVPNGateway: %v", err)
	}

	if err := m.DeleteVPNConnection(ctx, vpn.ID); err != nil {
		t.Fatalf("DeleteVPNConnection: %v", err)
	}

	// Delete the gateways and confirm the read-back is empty.
	if err := m.DeleteVPNGateway(ctx, vgw.ID); err != nil {
		t.Fatalf("DeleteVPNGateway: %v", err)
	}

	if err := m.DeleteCustomerGateway(ctx, cgw.ID); err != nil {
		t.Fatalf("DeleteCustomerGateway: %v", err)
	}

	if got, _ := m.DescribeCustomerGateways(ctx, nil); len(got) != 0 {
		t.Fatalf("customer gateway survived delete: %+v", got)
	}

	if got, _ := m.DescribeVPNGateways(ctx, nil); len(got) != 0 {
		t.Fatalf("vpn gateway survived delete: %+v", got)
	}

	// Deleting a missing connection is a NotFound.
	if err := m.DeleteVPNConnection(ctx, "vpn-nope"); !cerrors.IsNotFound(err) {
		t.Fatalf("delete missing vpn: got %v, want NotFound", err)
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

	// Modify: swap the entry, version bumps.
	mod, err := m.ModifyManagedPrefixList(ctx, pl.ID,
		[]driver.PrefixListEntry{{CIDR: "172.16.0.0/12"}}, []string{"10.0.0.0/8"})
	if err != nil || mod.Version != 2 {
		t.Fatalf("ModifyManagedPrefixList: %v %+v", err, mod)
	}

	entries2, _ := m.GetManagedPrefixListEntries(ctx, pl.ID)
	if len(entries2) != 1 || entries2[0].CIDR != "172.16.0.0/12" {
		t.Fatalf("prefix list after modify: %+v", entries2)
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

	// Endpoint-service permissions: add then remove.
	if err := m.ModifyVPCEndpointServicePermissions(ctx, svc.ID,
		[]string{"arn:aws:iam::111122223333:root", "arn:aws:iam::444455556666:root"}, nil); err != nil {
		t.Fatalf("ModifyVPCEndpointServicePermissions add: %v", err)
	}

	perms, _ := m.DescribeVPCEndpointServicePermissions(ctx, svc.ID)
	if len(perms) != 2 {
		t.Fatalf("endpoint service perms after add: %+v", perms)
	}

	if err := m.ModifyVPCEndpointServicePermissions(ctx, svc.ID,
		nil, []string{"arn:aws:iam::444455556666:root"}); err != nil {
		t.Fatalf("ModifyVPCEndpointServicePermissions remove: %v", err)
	}

	perms, _ = m.DescribeVPCEndpointServicePermissions(ctx, svc.ID)
	if len(perms) != 1 || perms[0] != "arn:aws:iam::111122223333:root" {
		t.Fatalf("endpoint service perms after remove: %+v", perms)
	}

	// Client VPN. Authentication options are required.
	if _, err := m.CreateClientVPNEndpoint(ctx, driver.ClientVPNEndpointConfig{
		ClientCIDRBlock: "10.100.0.0/16", ServerCertificateARN: "arn:aws:acm:us-east-1:0:certificate/abc",
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("client vpn without auth: got %v, want InvalidArgument", err)
	}

	ep, err := m.CreateClientVPNEndpoint(ctx, driver.ClientVPNEndpointConfig{
		ClientCIDRBlock: "10.100.0.0/16", ServerCertificateARN: "arn:aws:acm:us-east-1:0:certificate/abc",
		AuthenticationTypes: []string{"certificate-authentication"},
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

	nets, err := m.DescribeClientVPNTargetNetworks(ctx, ep.ID)
	if err != nil || len(nets) != 1 {
		t.Fatalf("DescribeClientVPNTargetNetworks: %v %+v", err, nets)
	}

	// Authorization rules.
	if _, err := m.AuthorizeClientVPNIngress(ctx, ep.ID, "10.0.0.0/16", "", true); err != nil {
		t.Fatalf("AuthorizeClientVPNIngress: %v", err)
	}

	rules, _ := m.DescribeClientVPNAuthorizationRules(ctx, ep.ID)
	if len(rules) != 1 || rules[0].TargetCIDR != "10.0.0.0/16" {
		t.Fatalf("auth rules: %+v", rules)
	}

	if err := m.RevokeClientVPNIngress(ctx, ep.ID, "10.0.0.0/16"); err != nil {
		t.Fatalf("RevokeClientVPNIngress: %v", err)
	}

	if rules, _ := m.DescribeClientVPNAuthorizationRules(ctx, ep.ID); len(rules) != 0 {
		t.Fatalf("auth rule survived revoke: %+v", rules)
	}

	// Routes.
	if _, err := m.CreateClientVPNRoute(ctx, ep.ID, "0.0.0.0/0", subnetID); err != nil {
		t.Fatalf("CreateClientVPNRoute: %v", err)
	}

	routes, _ := m.DescribeClientVPNRoutes(ctx, ep.ID)
	if len(routes) != 1 || routes[0].DestinationCIDR != "0.0.0.0/0" {
		t.Fatalf("client vpn routes: %+v", routes)
	}

	if err := m.DeleteClientVPNRoute(ctx, ep.ID, "0.0.0.0/0", subnetID); err != nil {
		t.Fatalf("DeleteClientVPNRoute: %v", err)
	}

	if err := m.DisassociateClientVPNTargetNetwork(ctx, ep.ID, assoc.AssociationID); err != nil {
		t.Fatalf("DisassociateClientVPNTargetNetwork: %v", err)
	}

	// Delete the remaining resources and confirm the read-back halves.
	if _, err := m.DeleteManagedPrefixList(ctx, pl.ID); err != nil {
		t.Fatalf("DeleteManagedPrefixList: %v", err)
	}

	if got, _ := m.DescribeManagedPrefixLists(ctx, nil); len(got) != 0 {
		t.Fatalf("prefix list survived delete: %+v", got)
	}

	if err := m.DeleteEgressOnlyInternetGateway(ctx, eigw.ID); err != nil {
		t.Fatalf("DeleteEgressOnlyInternetGateway: %v", err)
	}

	if err := m.DeleteVPCEndpointServiceConfiguration(ctx, svc.ID); err != nil {
		t.Fatalf("DeleteVPCEndpointServiceConfiguration: %v", err)
	}

	if got, _ := m.DescribeVPCEndpointServiceConfigurations(ctx, nil); len(got) != 0 {
		t.Fatalf("endpoint service survived delete: %+v", got)
	}

	if err := m.DeleteClientVPNEndpoint(ctx, ep.ID); err != nil {
		t.Fatalf("DeleteClientVPNEndpoint: %v", err)
	}

	if got, _ := m.DescribeClientVPNEndpoints(ctx, nil); len(got) != 0 {
		t.Fatalf("client vpn endpoint survived delete: %+v", got)
	}
}

// TestNetworkingConcurrentReadWrite exercises the reader RLock vs mutator Lock
// discipline under the race detector: an in-place VPN-gateway mutation
// (Attach/Detach) must not race a concurrent Describe. Meaningful with -race.
func TestNetworkingConcurrentReadWrite(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	vpcID, _ := mustVPC(t, m)

	vgw, err := m.CreateVPNGateway(ctx, driver.VPNGatewayConfig{})
	if err != nil {
		t.Fatalf("CreateVPNGateway: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)

		go func() {
			defer wg.Done()

			_, _ = m.AttachVPNGateway(ctx, vgw.ID, vpcID)
			_ = m.DetachVPNGateway(ctx, vgw.ID, vpcID)
		}()

		go func() {
			defer wg.Done()

			_, _ = m.DescribeVPNGateways(ctx, []string{vgw.ID})
		}()
	}

	wg.Wait()
}

func TestIPAMLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	ipam, err := m.CreateIpam(ctx, driver.IpamConfig{Description: "corp"})
	if err != nil || ipam.PublicDefaultScopeID == "" || ipam.PrivateDefaultScopeID == "" || ipam.ScopeCount != 2 {
		t.Fatalf("CreateIpam: %v %+v", err, ipam)
	}

	// Default scopes are discoverable.
	if got, _ := m.DescribeIpamScopes(ctx, nil); len(got) != 2 {
		t.Fatalf("default scopes: %+v", got)
	}

	// Creating a pool in a missing scope is rejected.
	if _, err := m.CreateIpamPool(ctx, driver.IpamPoolConfig{IpamScopeID: "ipam-scope-nope"}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("pool in missing scope: got %v, want InvalidArgument", err)
	}

	pool, err := m.CreateIpamPool(ctx, driver.IpamPoolConfig{IpamScopeID: ipam.PrivateDefaultScopeID, AddressFamily: "ipv4"})
	if err != nil || pool.State != "create-complete" {
		t.Fatalf("CreateIpamPool: %v %+v", err, pool)
	}

	// Provision supply, allocate, read back.
	if _, err := m.ProvisionIpamPoolCidr(ctx, pool.ID, "10.0.0.0/16", 0); err != nil {
		t.Fatalf("ProvisionIpamPoolCidr: %v", err)
	}

	if cidrs, _ := m.GetIpamPoolCidrs(ctx, pool.ID); len(cidrs) != 1 || cidrs[0].CIDR != "10.0.0.0/16" {
		t.Fatalf("GetIpamPoolCidrs: %+v", cidrs)
	}

	alloc, err := m.AllocateIpamPoolCidr(ctx, driver.AllocateIpamPoolCidrConfig{IpamPoolID: pool.ID, CIDR: "10.0.1.0/24"})
	if err != nil {
		t.Fatalf("AllocateIpamPoolCidr: %v", err)
	}

	// Netmask-only allocation (the standard AWS pattern) must derive a real,
	// non-empty CIDR of the requested size from the pool's supply, not the
	// empty string — otherwise downstream reads and AWS/IPAM metrics corrupt.
	nmAlloc, err := m.AllocateIpamPoolCidr(ctx, driver.AllocateIpamPoolCidrConfig{IpamPoolID: pool.ID, NetmaskLength: 24})
	if err != nil {
		t.Fatalf("AllocateIpamPoolCidr(netmask): %v", err)
	}

	if _, ipnet, perr := net.ParseCIDR(nmAlloc.CIDR); perr != nil {
		t.Fatalf("netmask-only allocation returned invalid CIDR %q: %v", nmAlloc.CIDR, perr)
	} else if ones, _ := ipnet.Mask.Size(); ones != 24 {
		t.Fatalf("derived CIDR %q is not a /24", nmAlloc.CIDR)
	}

	if nmAlloc.CIDR == "10.0.1.0/24" {
		t.Fatalf("derived CIDR overlaps the existing allocation: %q", nmAlloc.CIDR)
	}

	// Netmask-only provisioning likewise derives a concrete CIDR.
	nmProv, err := m.ProvisionIpamPoolCidr(ctx, pool.ID, "", 24)
	if err != nil {
		t.Fatalf("ProvisionIpamPoolCidr(netmask): %v", err)
	}

	if nmProv.CIDR == "" {
		t.Fatal("netmask-only provisioning returned an empty CIDR")
	}

	// Restore the pool to a single provisioned CIDR + single allocation so the
	// assertions below (delete-guard, allocation count) still hold.
	if err := m.ReleaseIpamPoolAllocation(ctx, pool.ID, nmAlloc.ID); err != nil {
		t.Fatalf("ReleaseIpamPoolAllocation(netmask): %v", err)
	}

	if _, err := m.DeprovisionIpamPoolCidr(ctx, pool.ID, nmProv.CIDR); err != nil {
		t.Fatalf("DeprovisionIpamPoolCidr(netmask): %v", err)
	}

	// A pool with a live allocation cannot be deleted.
	if _, err := m.DeleteIpamPool(ctx, pool.ID); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("delete pool with allocation: got %v, want FailedPrecondition", err)
	}

	if allocs, _ := m.GetIpamPoolAllocations(ctx, pool.ID); len(allocs) != 1 {
		t.Fatalf("GetIpamPoolAllocations: %+v", allocs)
	}

	if _, err := m.ModifyIpamPoolAllocation(ctx, alloc.ID, "updated"); err != nil {
		t.Fatalf("ModifyIpamPoolAllocation: %v", err)
	}

	if err := m.ReleaseIpamPoolAllocation(ctx, pool.ID, alloc.ID); err != nil {
		t.Fatalf("ReleaseIpamPoolAllocation: %v", err)
	}

	// Releasing again is NotFound.
	if err := m.ReleaseIpamPoolAllocation(ctx, pool.ID, alloc.ID); !cerrors.IsNotFound(err) {
		t.Fatalf("release twice: got %v, want NotFound", err)
	}

	if _, err := m.DeprovisionIpamPoolCidr(ctx, pool.ID, "10.0.0.0/16"); err != nil {
		t.Fatalf("DeprovisionIpamPoolCidr: %v", err)
	}

	if _, err := m.DeleteIpamPool(ctx, pool.ID); err != nil {
		t.Fatalf("DeleteIpamPool: %v", err)
	}

	// An IPAM with a non-default scope cannot be deleted until the scope is gone.
	extra, err := m.CreateIpamScope(ctx, driver.IpamScopeConfig{IpamID: ipam.ID})
	if err != nil {
		t.Fatalf("CreateIpamScope: %v", err)
	}

	if _, err := m.DeleteIpam(ctx, ipam.ID); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("delete ipam with scope: got %v, want FailedPrecondition", err)
	}

	if _, err := m.DeleteIpamScope(ctx, extra.ID); err != nil {
		t.Fatalf("DeleteIpamScope: %v", err)
	}

	if _, err := m.DeleteIpam(ctx, ipam.ID); err != nil {
		t.Fatalf("DeleteIpam: %v", err)
	}

	if got, _ := m.DescribeIpams(ctx, nil); len(got) != 0 {
		t.Fatalf("ipam survived delete: %+v", got)
	}
}

// TestIPAMProvisionNoCrossPoolOverlap guards the review fix: two top-level
// pools each provisioning a netmask-only /16 must receive DISTINCT,
// non-overlapping CIDRs — both carve from the same shared base, so the
// second must skip the first's block instead of both getting 10.0.0.0/16.
func TestIPAMProvisionNoCrossPoolOverlap(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	ip, err := m.CreateIpam(ctx, driver.IpamConfig{})
	if err != nil {
		t.Fatalf("CreateIpam: %v", err)
	}

	poolA, err := m.CreateIpamPool(ctx, driver.IpamPoolConfig{IpamScopeID: ip.PrivateDefaultScopeID, AddressFamily: "ipv4"})
	if err != nil {
		t.Fatalf("CreateIpamPool A: %v", err)
	}

	poolB, err := m.CreateIpamPool(ctx, driver.IpamPoolConfig{IpamScopeID: ip.PrivateDefaultScopeID, AddressFamily: "ipv4"})
	if err != nil {
		t.Fatalf("CreateIpamPool B: %v", err)
	}

	a, err := m.ProvisionIpamPoolCidr(ctx, poolA.ID, "", 16)
	if err != nil {
		t.Fatalf("Provision A: %v", err)
	}

	b, err := m.ProvisionIpamPoolCidr(ctx, poolB.ID, "", 16)
	if err != nil {
		t.Fatalf("Provision B: %v", err)
	}

	if a.CIDR == b.CIDR {
		t.Fatalf("cross-pool overlap: both pools provisioned %q", a.CIDR)
	}

	sa, ea, _ := ipv4Range(a.CIDR)
	sb, eb, _ := ipv4Range(b.CIDR)

	if sa <= eb && sb <= ea {
		t.Fatalf("provisioned CIDRs overlap: %q and %q", a.CIDR, b.CIDR)
	}
}

// TestIPAMConcurrentAccess exercises the IPAM locking under -race.
func TestIPAMConcurrentAccess(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	ipam, err := m.CreateIpam(ctx, driver.IpamConfig{})
	if err != nil {
		t.Fatalf("CreateIpam: %v", err)
	}

	pool, err := m.CreateIpamPool(ctx, driver.IpamPoolConfig{IpamScopeID: ipam.PrivateDefaultScopeID})
	if err != nil {
		t.Fatalf("CreateIpamPool: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)

		go func() {
			defer wg.Done()

			_, _ = m.ProvisionIpamPoolCidr(ctx, pool.ID, "10.0.0.0/16", 0)
			_, _ = m.ModifyIpamPool(ctx, pool.ID, "x")
		}()

		go func() {
			defer wg.Done()

			_, _ = m.GetIpamPoolCidrs(ctx, pool.ID)
			_, _ = m.DescribeIpamPools(ctx, []string{pool.ID})
		}()
	}

	wg.Wait()
}

func TestIPAMFullProvider(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	// Seed a VPC + subnet so resource CIDRs / discovery / metrics have input.
	v, _ := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	_, _ = m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: v.ID, CIDRBlock: "10.0.0.0/24"})

	ipam, err := m.CreateIpam(ctx, driver.IpamConfig{})
	if err != nil || ipam.DefaultResourceDiscoveryID == "" || ipam.ResourceDiscoveryAssociationCount != 1 {
		t.Fatalf("CreateIpam default RD: %v %+v", err, ipam)
	}

	// Resource CIDRs + history derive from the VPC/subnet.
	rc, err := m.GetIpamResourceCidrs(ctx, ipam.PrivateDefaultScopeID, "")
	if err != nil || len(rc) != 2 {
		t.Fatalf("GetIpamResourceCidrs: %v %+v", err, rc)
	}

	// ModifyIpamResourceCidr must PERSIST: unmonitoring a resource has to
	// survive the next (freshly-derived) read, not silently revert.
	if _, err := m.ModifyIpamResourceCidr(ctx, rc[0].ResourceID, ipam.PrivateDefaultScopeID, "", false); err != nil {
		t.Fatalf("ModifyIpamResourceCidr: %v", err)
	}

	after, err := m.GetIpamResourceCidrs(ctx, ipam.PrivateDefaultScopeID, rc[0].ResourceID)
	if err != nil || len(after) != 1 {
		t.Fatalf("GetIpamResourceCidrs(after modify): %v %+v", err, after)
	}

	if after[0].ManagementState != "unmanaged" {
		t.Fatalf("unmonitor did not persist: ManagementState=%q, want unmanaged", after[0].ManagementState)
	}

	if hist, _ := m.GetIpamAddressHistory(ctx, "10.0.0.0/16", ""); len(hist) != 1 {
		t.Fatalf("GetIpamAddressHistory: %+v", hist)
	}

	// Resource discovery: default RD is discoverable + discovered getters work.
	if rds, _ := m.DescribeIpamResourceDiscoveries(ctx, nil); len(rds) != 1 {
		t.Fatalf("DescribeIpamResourceDiscoveries: %+v", rds)
	}

	if accts, _ := m.GetIpamDiscoveredAccounts(ctx, ipam.DefaultResourceDiscoveryID, ""); len(accts) != 1 {
		t.Fatalf("GetIpamDiscoveredAccounts: %+v", accts)
	}

	if dcidrs, _ := m.GetIpamDiscoveredResourceCidrs(ctx, ipam.DefaultResourceDiscoveryID, ""); len(dcidrs) != 2 {
		t.Fatalf("GetIpamDiscoveredResourceCidrs: %+v", dcidrs)
	}

	// A non-default resource discovery can be created + deleted.
	rd, err := m.CreateIpamResourceDiscovery(ctx, driver.IpamResourceDiscoveryConfig{Description: "extra"})
	if err != nil {
		t.Fatalf("CreateIpamResourceDiscovery: %v", err)
	}

	if _, err := m.DeleteIpamResourceDiscovery(ctx, rd.ID); err != nil {
		t.Fatalf("DeleteIpamResourceDiscovery: %v", err)
	}

	// BYOASN + BYOIP.
	if _, err := m.ProvisionIpamByoasn(ctx, ipam.ID, "64512"); err != nil {
		t.Fatalf("ProvisionIpamByoasn: %v", err)
	}

	if _, err := m.ProvisionByoipCidr(ctx, "203.0.113.0/24", "byoip"); err != nil {
		t.Fatalf("ProvisionByoipCidr: %v", err)
	}

	if _, err := m.AdvertiseByoipCidr(ctx, "203.0.113.0/24"); err != nil {
		t.Fatalf("AdvertiseByoipCidr: %v", err)
	}

	// BYOASN association validates both sides: a provisioned CIDR associates,
	// an unprovisioned one is rejected, and disassociating an unknown ASN errs.
	if _, err := m.AssociateIpamByoasn(ctx, "64512", "203.0.113.0/24"); err != nil {
		t.Fatalf("AssociateIpamByoasn: %v", err)
	}

	if _, err := m.AssociateIpamByoasn(ctx, "64512", "198.51.100.0/24"); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("associate unprovisioned cidr: got %v, want InvalidArgument", err)
	}

	if _, err := m.DisassociateIpamByoasn(ctx, "64999", "203.0.113.0/24"); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("disassociate unknown asn: got %v, want InvalidArgument", err)
	}

	if _, err := m.DisassociateIpamByoasn(ctx, "64512", "203.0.113.0/24"); err != nil {
		t.Fatalf("DisassociateIpamByoasn: %v", err)
	}

	// Advertised CIDR cannot be deprovisioned.
	if _, err := m.DeprovisionByoipCidr(ctx, "203.0.113.0/24"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("deprovision advertised: got %v, want FailedPrecondition", err)
	}

	// Prefix-list resolver + target + verification token.
	res, err := m.CreateIpamPrefixListResolver(ctx, ipam.ID, "ipv4", "res", nil)
	if err != nil {
		t.Fatalf("CreateIpamPrefixListResolver: %v", err)
	}

	// The target must reference an existing managed prefix list.
	targetPL, err := m.CreateManagedPrefixList(ctx, driver.PrefixListConfig{
		Name: "resolver-target", MaxEntries: 5, Entries: []driver.PrefixListEntry{{CIDR: "10.1.0.0/16"}},
	})
	if err != nil {
		t.Fatalf("CreateManagedPrefixList: %v", err)
	}

	if _, err := m.CreateIpamPrefixListResolverTarget(ctx, res.ID, targetPL.ID, "", 1, true, nil); err != nil {
		t.Fatalf("CreateIpamPrefixListResolverTarget: %v", err)
	}

	// A target referencing a non-existent prefix list is rejected.
	if _, err := m.CreateIpamPrefixListResolverTarget(ctx, res.ID, "pl-does-not-exist", "", 1, true, nil); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("target with unknown prefix list: got %v, want InvalidArgument", err)
	}

	// Resolver with a target cannot be deleted.
	if _, err := m.DeleteIpamPrefixListResolver(ctx, res.ID); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("delete resolver with target: got %v, want FailedPrecondition", err)
	}

	if _, err := m.CreateIpamExternalResourceVerificationToken(ctx, ipam.ID, "tok", nil); err != nil {
		t.Fatalf("CreateIpamExternalResourceVerificationToken: %v", err)
	}

	// Policy + org admin.
	pol, err := m.CreateIpamPolicy(ctx, ipam.ID, nil)
	if err != nil {
		t.Fatalf("CreateIpamPolicy: %v", err)
	}

	if _, err := m.EnableIpamPolicy(ctx, pol.ID, ""); err != nil {
		t.Fatalf("EnableIpamPolicy: %v", err)
	}

	if id, enabled, _, _ := m.GetEnabledIpamPolicy(ctx); !enabled || id != pol.ID {
		t.Fatalf("GetEnabledIpamPolicy: id=%q enabled=%v", id, enabled)
	}

	if ok, err := m.EnableIpamOrganizationAdminAccount(ctx, "111122223333"); err != nil || !ok {
		t.Fatalf("EnableIpamOrganizationAdminAccount: %v %v", ok, err)
	}

	// Metrics derive from state: TotalActiveIpCount + VpcIPUsage present.
	metrics := m.IpamMetrics(ctx)
	seen := map[string]bool{}
	for _, mtr := range metrics {
		seen[mtr.MetricName] = true
	}

	if !seen["TotalActiveIpCount"] || !seen["VpcIPUsage"] || !seen["ManagedResourceCidrs"] {
		t.Fatalf("expected IPAM metrics, got %v", seen)
	}

	// Pool metrics appear once a pool with supply + an allocation exists.
	pool, _ := m.CreateIpamPool(ctx, driver.IpamPoolConfig{IpamScopeID: ipam.PrivateDefaultScopeID})
	_, _ = m.ProvisionIpamPoolCidr(ctx, pool.ID, "10.1.0.0/16", 0)
	_, _ = m.AllocateIpamPoolCidr(ctx, driver.AllocateIpamPoolCidrConfig{IpamPoolID: pool.ID, CIDR: "10.1.1.0/24"})

	seen = map[string]bool{}
	for _, mtr := range m.IpamMetrics(ctx) {
		seen[mtr.MetricName] = true
	}

	if !seen["PercentAssigned"] || !seen["PercentAvailable"] {
		t.Fatalf("expected pool metrics, got %v", seen)
	}
}

func TestTrafficMirroringLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	target, err := m.CreateTrafficMirrorTarget(ctx, driver.TrafficMirrorTargetConfig{
		NetworkInterfaceID: "eni-123", Description: "t", Tags: map[string]string{"Name": "tm"},
	})
	if err != nil || target.ID == "" || target.Type != "network-interface" {
		t.Fatalf("CreateTrafficMirrorTarget: %v %+v", err, target)
	}

	filter, err := m.CreateTrafficMirrorFilter(ctx, "f", nil)
	if err != nil || filter.ID == "" {
		t.Fatalf("CreateTrafficMirrorFilter: %v %+v", err, filter)
	}

	rule, err := m.CreateTrafficMirrorFilterRule(ctx, driver.TrafficMirrorFilterRuleConfig{
		FilterID: filter.ID, TrafficDirection: "ingress", RuleNumber: 100, RuleAction: "accept",
		Protocol: 6, SourceCIDR: "0.0.0.0/0", DestinationCIDR: "10.0.0.0/16",
		DestinationPortRange: &driver.TrafficMirrorPortRange{FromPort: 80, ToPort: 80},
	})
	if err != nil || rule.ID == "" || rule.RuleAction != "accept" {
		t.Fatalf("CreateTrafficMirrorFilterRule: %v %+v", err, rule)
	}

	rules, err := m.DescribeTrafficMirrorFilterRules(ctx, filter.ID, nil)
	if err != nil || len(rules) != 1 || rules[0].DestinationPortRange == nil {
		t.Fatalf("DescribeTrafficMirrorFilterRules: %v %+v", err, rules)
	}

	mod, err := m.ModifyTrafficMirrorFilterRule(ctx, rule.ID,
		driver.TrafficMirrorFilterRuleConfig{RuleAction: "reject"}, []string{"destination-port-range"})
	if err != nil || mod.RuleAction != "reject" || mod.DestinationPortRange != nil {
		t.Fatalf("ModifyTrafficMirrorFilterRule: %v %+v", err, mod)
	}

	session, err := m.CreateTrafficMirrorSession(ctx, driver.TrafficMirrorSessionConfig{
		NetworkInterfaceID: "eni-999", TrafficMirrorTargetID: target.ID,
		TrafficMirrorFilterID: filter.ID, SessionNumber: 1,
	})
	if err != nil || session.ID == "" || session.VirtualNetworkID != defaultVirtualNetworkID {
		t.Fatalf("CreateTrafficMirrorSession: %v %+v", err, session)
	}

	if _, err := m.CreateTrafficMirrorSession(ctx, driver.TrafficMirrorSessionConfig{
		TrafficMirrorTargetID: "tmt-missing", TrafficMirrorFilterID: filter.ID, SessionNumber: 1,
	}); !cerrors.IsNotFound(err) {
		t.Fatalf("expected NotFound for missing target, got %v", err)
	}

	// Modify must re-validate a re-pointed target/filter, matching Create — a
	// Modify can't bind a live session to a nonexistent target or filter.
	if _, err := m.ModifyTrafficMirrorSession(ctx, session.ID,
		driver.TrafficMirrorSessionConfig{TrafficMirrorTargetID: "tmt-missing"}, nil); !cerrors.IsNotFound(err) {
		t.Fatalf("expected NotFound modifying session to missing target, got %v", err)
	}

	if _, err := m.ModifyTrafficMirrorSession(ctx, session.ID,
		driver.TrafficMirrorSessionConfig{TrafficMirrorFilterID: "tmf-missing"}, nil); !cerrors.IsNotFound(err) {
		t.Fatalf("expected NotFound modifying session to missing filter, got %v", err)
	}

	// A valid modify that doesn't re-point leaves the existing refs intact.
	if mod, err := m.ModifyTrafficMirrorSession(ctx, session.ID,
		driver.TrafficMirrorSessionConfig{SessionNumber: 7}, nil); err != nil ||
		mod.SessionNumber != 7 || mod.TrafficMirrorTargetID != target.ID ||
		mod.TrafficMirrorFilterID != filter.ID {
		t.Fatalf("ModifyTrafficMirrorSession valid update: %v %+v", err, mod)
	}

	if err := m.DeleteTrafficMirrorSession(ctx, session.ID); err != nil {
		t.Fatalf("DeleteTrafficMirrorSession: %v", err)
	}

	if err := m.DeleteTrafficMirrorFilterRule(ctx, rule.ID); err != nil {
		t.Fatalf("DeleteTrafficMirrorFilterRule: %v", err)
	}

	if err := m.DeleteTrafficMirrorFilter(ctx, filter.ID); err != nil {
		t.Fatalf("DeleteTrafficMirrorFilter: %v", err)
	}

	if err := m.DeleteTrafficMirrorTarget(ctx, target.ID); err != nil {
		t.Fatalf("DeleteTrafficMirrorTarget: %v", err)
	}

	if err := m.DeleteTrafficMirrorTarget(ctx, target.ID); !cerrors.IsNotFound(err) {
		t.Fatalf("expected NotFound on second delete, got %v", err)
	}
}

func TestNetworkInsightsReachability(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	path, err := m.CreateNetworkInsightsPath(ctx, driver.NetworkInsightsPathConfig{
		Protocol: "tcp", Source: "igw-1", Destination: "eni-1", DestinationPort: 443,
	})
	if err != nil || path.ID == "" || path.ARN == "" {
		t.Fatalf("CreateNetworkInsightsPath: %v %+v", err, path)
	}

	if _, err := m.CreateNetworkInsightsPath(ctx, driver.NetworkInsightsPathConfig{}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("expected InvalidArgument for missing source, got %v", err)
	}

	analysis, err := m.StartNetworkInsightsAnalysis(ctx, driver.NetworkInsightsAnalysisConfig{PathID: path.ID})
	if err != nil || analysis.Status != "succeeded" || !analysis.NetworkPathFound {
		t.Fatalf("StartNetworkInsightsAnalysis: %v %+v", err, analysis)
	}

	got, err := m.DescribeNetworkInsightsAnalyses(ctx, nil, path.ID)
	if err != nil || len(got) != 1 {
		t.Fatalf("DescribeNetworkInsightsAnalyses: %v %+v", err, got)
	}

	// Deleting the path cascades to its analyses.
	if err := m.DeleteNetworkInsightsPath(ctx, path.ID); err != nil {
		t.Fatalf("DeleteNetworkInsightsPath: %v", err)
	}

	if got, _ := m.DescribeNetworkInsightsAnalyses(ctx, nil, ""); len(got) != 0 {
		t.Fatalf("expected analyses cascaded on path delete, got %+v", got)
	}
}

func TestNetworkInsightsAccessScope(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	scope, err := m.CreateNetworkInsightsAccessScope(ctx, driver.NetworkInsightsAccessScopeConfig{
		MatchPaths: []driver.AccessScopePath{{
			Source: &driver.AccessScopeStatement{
				ResourceStatement: &driver.AccessScopeResourceStatement{ResourceTypes: []string{"AWS::EC2::InternetGateway"}},
			},
		}},
	})
	if err != nil || scope.ID == "" || len(scope.MatchPaths) != 1 {
		t.Fatalf("CreateNetworkInsightsAccessScope: %v %+v", err, scope)
	}

	content, err := m.GetNetworkInsightsAccessScopeContent(ctx, scope.ID)
	if err != nil || len(content.MatchPaths) != 1 ||
		content.MatchPaths[0].Source.ResourceStatement.ResourceTypes[0] != "AWS::EC2::InternetGateway" {
		t.Fatalf("GetNetworkInsightsAccessScopeContent: %v %+v", err, content)
	}

	analysis, err := m.StartNetworkInsightsAccessScopeAnalysis(ctx, scope.ID, nil)
	if err != nil || analysis.Status != "succeeded" {
		t.Fatalf("StartNetworkInsightsAccessScopeAnalysis: %v %+v", err, analysis)
	}

	findings, status, err := m.GetNetworkInsightsAccessScopeAnalysisFindings(ctx, analysis.ID)
	if err != nil || status != "succeeded" || len(findings) != 0 {
		t.Fatalf("GetNetworkInsightsAccessScopeAnalysisFindings: %v %q %+v", err, status, findings)
	}

	if err := m.DeleteNetworkInsightsAccessScope(ctx, scope.ID); err != nil {
		t.Fatalf("DeleteNetworkInsightsAccessScope: %v", err)
	}

	if got, _ := m.DescribeNetworkInsightsAccessScopeAnalyses(ctx, nil, ""); len(got) != 0 {
		t.Fatalf("expected scope analyses cascaded on delete, got %+v", got)
	}
}

func TestVPCBlockPublicAccess(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	vpcID, subnetID := mustVPC(t, m)

	opts, err := m.DescribeVPCBlockPublicAccessOptions(ctx)
	if err != nil || opts.InternetGatewayBlockMode != "off" || opts.State != "default-state" {
		t.Fatalf("DescribeVPCBlockPublicAccessOptions default: %v %+v", err, opts)
	}

	opts, err = m.ModifyVPCBlockPublicAccessOptions(ctx, "block-bidirectional")
	if err != nil || opts.InternetGatewayBlockMode != "block-bidirectional" || opts.State != "update-complete" {
		t.Fatalf("ModifyVPCBlockPublicAccessOptions: %v %+v", err, opts)
	}

	excl, err := m.CreateVPCBlockPublicAccessExclusion(ctx, driver.VPCBlockPublicAccessExclusionConfig{
		SubnetID: subnetID, InternetGatewayExclusionMode: "allow-bidirectional",
	})
	if err != nil || excl.ExclusionID == "" || excl.State != "create-complete" {
		t.Fatalf("CreateVPCBlockPublicAccessExclusion: %v %+v", err, excl)
	}

	if _, err := m.CreateVPCBlockPublicAccessExclusion(ctx, driver.VPCBlockPublicAccessExclusionConfig{
		VPCID: "vpc-missing", InternetGatewayExclusionMode: "allow-egress",
	}); !cerrors.IsNotFound(err) {
		t.Fatalf("expected NotFound for missing vpc, got %v", err)
	}

	if _, err := m.CreateVPCBlockPublicAccessExclusion(ctx, driver.VPCBlockPublicAccessExclusionConfig{
		VPCID: vpcID, InternetGatewayExclusionMode: "allow-egress",
	}); err != nil {
		t.Fatalf("CreateVPCBlockPublicAccessExclusion vpc: %v", err)
	}

	mod, err := m.ModifyVPCBlockPublicAccessExclusion(ctx, excl.ExclusionID, "allow-egress")
	if err != nil || mod.InternetGatewayExclusionMode != "allow-egress" {
		t.Fatalf("ModifyVPCBlockPublicAccessExclusion: %v %+v", err, mod)
	}

	list, err := m.DescribeVPCBlockPublicAccessExclusions(ctx, nil)
	if err != nil || len(list) != 2 {
		t.Fatalf("DescribeVPCBlockPublicAccessExclusions: %v %+v", err, list)
	}

	del, err := m.DeleteVPCBlockPublicAccessExclusion(ctx, excl.ExclusionID)
	if err != nil || del.State != "delete-complete" {
		t.Fatalf("DeleteVPCBlockPublicAccessExclusion: %v %+v", err, del)
	}

	if _, err := m.DeleteVPCBlockPublicAccessExclusion(ctx, excl.ExclusionID); !cerrors.IsNotFound(err) {
		t.Fatalf("expected NotFound on second delete, got %v", err)
	}
}
