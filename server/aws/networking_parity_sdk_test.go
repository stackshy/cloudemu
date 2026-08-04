package aws_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestEC2NetworkingParitySDK drives the real aws-sdk-go-v2 EC2 client against
// the new AWS-only networking capabilities (transit gateway, VPN, DHCP options,
// managed prefix lists, egress-only IGW, endpoint services, Client VPN),
// proving the query-protocol XML round-trips.
func TestEC2NetworkingParitySDK(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	// Prerequisite VPC + subnet for attachment-style resources.
	vpcOut, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	require.NoError(t, err)
	vpcID := aws.ToString(vpcOut.Vpc.VpcId)

	subnetOut, err := client.CreateSubnet(ctx, &ec2.CreateSubnetInput{
		VpcId: aws.String(vpcID), CidrBlock: aws.String("10.0.1.0/24"),
	})
	require.NoError(t, err)
	subnetID := aws.ToString(subnetOut.Subnet.SubnetId)

	t.Run("transit gateway", func(t *testing.T) {
		tgw, err := client.CreateTransitGateway(ctx, &ec2.CreateTransitGatewayInput{
			Description: aws.String("hub"),
			Options:     &ec2types.TransitGatewayRequestOptions{AmazonSideAsn: aws.Int64(64513)},
		})
		require.NoError(t, err)
		tgwID := aws.ToString(tgw.TransitGateway.TransitGatewayId)
		assert.NotEmpty(t, tgwID)
		assert.EqualValues(t, 64513, aws.ToInt64(tgw.TransitGateway.Options.AmazonSideAsn))

		desc, err := client.DescribeTransitGateways(ctx, &ec2.DescribeTransitGatewaysInput{
			TransitGatewayIds: []string{tgwID},
		})
		require.NoError(t, err)
		require.Len(t, desc.TransitGateways, 1)

		att, err := client.CreateTransitGatewayVpcAttachment(ctx, &ec2.CreateTransitGatewayVpcAttachmentInput{
			TransitGatewayId: aws.String(tgwID), VpcId: aws.String(vpcID), SubnetIds: []string{subnetID},
		})
		require.NoError(t, err)
		assert.Equal(t, vpcID, aws.ToString(att.TransitGatewayVpcAttachment.VpcId))

		rt, err := client.CreateTransitGatewayRouteTable(ctx, &ec2.CreateTransitGatewayRouteTableInput{
			TransitGatewayId: aws.String(tgwID),
		})
		require.NoError(t, err)
		rtID := aws.ToString(rt.TransitGatewayRouteTable.TransitGatewayRouteTableId)
		assert.NotEmpty(t, rtID)
		attID := aws.ToString(att.TransitGatewayVpcAttachment.TransitGatewayAttachmentId)

		_, err = client.AssociateTransitGatewayRouteTable(ctx, &ec2.AssociateTransitGatewayRouteTableInput{
			TransitGatewayRouteTableId: aws.String(rtID), TransitGatewayAttachmentId: aws.String(attID),
		})
		require.NoError(t, err)

		_, err = client.CreateTransitGatewayRoute(ctx, &ec2.CreateTransitGatewayRouteInput{
			TransitGatewayRouteTableId: aws.String(rtID), DestinationCidrBlock: aws.String("10.1.0.0/16"),
			TransitGatewayAttachmentId: aws.String(attID),
		})
		require.NoError(t, err)

		routes, err := client.SearchTransitGatewayRoutes(ctx, &ec2.SearchTransitGatewayRoutesInput{
			TransitGatewayRouteTableId: aws.String(rtID),
			Filters:                    []ec2types.Filter{{Name: aws.String("state"), Values: []string{"active"}}},
		})
		require.NoError(t, err)
		require.Len(t, routes.Routes, 1)
		assert.Equal(t, "10.1.0.0/16", aws.ToString(routes.Routes[0].DestinationCidrBlock))

		_, err = client.EnableTransitGatewayRouteTablePropagation(ctx, &ec2.EnableTransitGatewayRouteTablePropagationInput{
			TransitGatewayRouteTableId: aws.String(rtID), TransitGatewayAttachmentId: aws.String(attID),
		})
		require.NoError(t, err)

		_, err = client.DeleteTransitGatewayRoute(ctx, &ec2.DeleteTransitGatewayRouteInput{
			TransitGatewayRouteTableId: aws.String(rtID), DestinationCidrBlock: aws.String("10.1.0.0/16"),
		})
		require.NoError(t, err)
	})

	t.Run("vpn", func(t *testing.T) {
		cgw, err := client.CreateCustomerGateway(ctx, &ec2.CreateCustomerGatewayInput{
			IpAddress: aws.String("203.0.113.10"), BgpAsn: aws.Int32(65000), Type: ec2types.GatewayTypeIpsec1,
		})
		require.NoError(t, err)
		cgwID := aws.ToString(cgw.CustomerGateway.CustomerGatewayId)

		vgw, err := client.CreateVpnGateway(ctx, &ec2.CreateVpnGatewayInput{Type: ec2types.GatewayTypeIpsec1})
		require.NoError(t, err)
		vgwID := aws.ToString(vgw.VpnGateway.VpnGatewayId)

		_, err = client.AttachVpnGateway(ctx, &ec2.AttachVpnGatewayInput{
			VpnGatewayId: aws.String(vgwID), VpcId: aws.String(vpcID),
		})
		require.NoError(t, err)

		vpn, err := client.CreateVpnConnection(ctx, &ec2.CreateVpnConnectionInput{
			CustomerGatewayId: aws.String(cgwID), VpnGatewayId: aws.String(vgwID), Type: aws.String("ipsec.1"),
		})
		require.NoError(t, err)
		assert.Equal(t, cgwID, aws.ToString(vpn.VpnConnection.CustomerGatewayId))
		vpnID := aws.ToString(vpn.VpnConnection.VpnConnectionId)

		_, err = client.CreateVpnConnectionRoute(ctx, &ec2.CreateVpnConnectionRouteInput{
			VpnConnectionId: aws.String(vpnID), DestinationCidrBlock: aws.String("192.168.0.0/16"),
		})
		require.NoError(t, err)

		desc, err := client.DescribeVpnConnections(ctx, &ec2.DescribeVpnConnectionsInput{})
		require.NoError(t, err)
		require.Len(t, desc.VpnConnections, 1)
		require.Len(t, desc.VpnConnections[0].Routes, 1)
		assert.Equal(t, "192.168.0.0/16", aws.ToString(desc.VpnConnections[0].Routes[0].DestinationCidrBlock))

		_, err = client.DeleteVpnConnectionRoute(ctx, &ec2.DeleteVpnConnectionRouteInput{
			VpnConnectionId: aws.String(vpnID), DestinationCidrBlock: aws.String("192.168.0.0/16"),
		})
		require.NoError(t, err)

		// Error path: modifying a nonexistent connection surfaces an SDK error
		// deserialized from the query-protocol error XML.
		_, err = client.ModifyVpnConnection(ctx, &ec2.ModifyVpnConnectionInput{
			VpnConnectionId: aws.String("vpn-does-not-exist"), VpnGatewayId: aws.String(vgwID),
		})
		require.Error(t, err, "expected error modifying unknown vpn connection")
	})

	t.Run("dhcp options", func(t *testing.T) {
		out, err := client.CreateDhcpOptions(ctx, &ec2.CreateDhcpOptionsInput{
			DhcpConfigurations: []ec2types.NewDhcpConfiguration{
				{Key: aws.String("domain-name-servers"), Values: []string{"10.0.0.2"}},
			},
		})
		require.NoError(t, err)
		id := aws.ToString(out.DhcpOptions.DhcpOptionsId)
		assert.NotEmpty(t, id)

		_, err = client.AssociateDhcpOptions(ctx, &ec2.AssociateDhcpOptionsInput{
			DhcpOptionsId: aws.String(id), VpcId: aws.String(vpcID),
		})
		require.NoError(t, err)
	})

	t.Run("managed prefix list", func(t *testing.T) {
		out, err := client.CreateManagedPrefixList(ctx, &ec2.CreateManagedPrefixListInput{
			PrefixListName: aws.String("corp"), MaxEntries: aws.Int32(10), AddressFamily: aws.String("IPv4"),
			Entries: []ec2types.AddPrefixListEntry{{Cidr: aws.String("10.0.0.0/8"), Description: aws.String("corp")}},
		})
		require.NoError(t, err)
		id := aws.ToString(out.PrefixList.PrefixListId)

		entries, err := client.GetManagedPrefixListEntries(ctx, &ec2.GetManagedPrefixListEntriesInput{
			PrefixListId: aws.String(id),
		})
		require.NoError(t, err)
		require.Len(t, entries.Entries, 1)
		assert.Equal(t, "10.0.0.0/8", aws.ToString(entries.Entries[0].Cidr))

		mod, err := client.ModifyManagedPrefixList(ctx, &ec2.ModifyManagedPrefixListInput{
			PrefixListId:  aws.String(id),
			AddEntries:    []ec2types.AddPrefixListEntry{{Cidr: aws.String("172.16.0.0/12")}},
			RemoveEntries: []ec2types.RemovePrefixListEntry{{Cidr: aws.String("10.0.0.0/8")}},
		})
		require.NoError(t, err)
		assert.NotNil(t, mod.PrefixList)

		entries2, err := client.GetManagedPrefixListEntries(ctx, &ec2.GetManagedPrefixListEntriesInput{
			PrefixListId: aws.String(id),
		})
		require.NoError(t, err)
		require.Len(t, entries2.Entries, 1)
		assert.Equal(t, "172.16.0.0/12", aws.ToString(entries2.Entries[0].Cidr))
	})

	t.Run("egress-only internet gateway", func(t *testing.T) {
		out, err := client.CreateEgressOnlyInternetGateway(ctx, &ec2.CreateEgressOnlyInternetGatewayInput{
			VpcId: aws.String(vpcID),
		})
		require.NoError(t, err)
		assert.NotEmpty(t, aws.ToString(out.EgressOnlyInternetGateway.EgressOnlyInternetGatewayId))
	})

	t.Run("vpc endpoint service", func(t *testing.T) {
		out, err := client.CreateVpcEndpointServiceConfiguration(ctx, &ec2.CreateVpcEndpointServiceConfigurationInput{
			NetworkLoadBalancerArns: []string{"arn:aws:elasticloadbalancing:us-east-1:000000000000:loadbalancer/net/x/1"},
		})
		require.NoError(t, err)
		svcID := aws.ToString(out.ServiceConfiguration.ServiceId)
		assert.NotEmpty(t, svcID)

		_, err = client.ModifyVpcEndpointServicePermissions(ctx, &ec2.ModifyVpcEndpointServicePermissionsInput{
			ServiceId:            aws.String(svcID),
			AddAllowedPrincipals: []string{"arn:aws:iam::111122223333:root"},
		})
		require.NoError(t, err)

		perms, err := client.DescribeVpcEndpointServicePermissions(ctx, &ec2.DescribeVpcEndpointServicePermissionsInput{
			ServiceId: aws.String(svcID),
		})
		require.NoError(t, err)
		require.Len(t, perms.AllowedPrincipals, 1)
		assert.Equal(t, "arn:aws:iam::111122223333:root", aws.ToString(perms.AllowedPrincipals[0].Principal))
	})

	t.Run("client vpn", func(t *testing.T) {
		out, err := client.CreateClientVpnEndpoint(ctx, &ec2.CreateClientVpnEndpointInput{
			ClientCidrBlock:      aws.String("10.100.0.0/16"),
			ServerCertificateArn: aws.String("arn:aws:acm:us-east-1:000000000000:certificate/abc"),
			AuthenticationOptions: []ec2types.ClientVpnAuthenticationRequest{
				{Type: ec2types.ClientVpnAuthenticationTypeCertificateAuthentication},
			},
			ConnectionLogOptions: &ec2types.ConnectionLogOptions{Enabled: aws.Bool(false)},
		})
		require.NoError(t, err)
		epID := aws.ToString(out.ClientVpnEndpointId)
		assert.NotEmpty(t, epID)

		assoc, err := client.AssociateClientVpnTargetNetwork(ctx, &ec2.AssociateClientVpnTargetNetworkInput{
			ClientVpnEndpointId: aws.String(epID), SubnetId: aws.String(subnetID),
		})
		require.NoError(t, err)
		assert.NotEmpty(t, aws.ToString(assoc.AssociationId))

		nets, err := client.DescribeClientVpnTargetNetworks(ctx, &ec2.DescribeClientVpnTargetNetworksInput{
			ClientVpnEndpointId: aws.String(epID),
		})
		require.NoError(t, err)
		require.Len(t, nets.ClientVpnTargetNetworks, 1)

		_, err = client.AuthorizeClientVpnIngress(ctx, &ec2.AuthorizeClientVpnIngressInput{
			ClientVpnEndpointId: aws.String(epID), TargetNetworkCidr: aws.String("10.0.0.0/16"),
			AuthorizeAllGroups: aws.Bool(true),
		})
		require.NoError(t, err)

		rules, err := client.DescribeClientVpnAuthorizationRules(ctx, &ec2.DescribeClientVpnAuthorizationRulesInput{
			ClientVpnEndpointId: aws.String(epID),
		})
		require.NoError(t, err)
		require.Len(t, rules.AuthorizationRules, 1)
		assert.Equal(t, "10.0.0.0/16", aws.ToString(rules.AuthorizationRules[0].DestinationCidr))

		_, err = client.CreateClientVpnRoute(ctx, &ec2.CreateClientVpnRouteInput{
			ClientVpnEndpointId: aws.String(epID), DestinationCidrBlock: aws.String("0.0.0.0/0"),
			TargetVpcSubnetId: aws.String(subnetID),
		})
		require.NoError(t, err)

		vpnRoutes, err := client.DescribeClientVpnRoutes(ctx, &ec2.DescribeClientVpnRoutesInput{
			ClientVpnEndpointId: aws.String(epID),
		})
		require.NoError(t, err)
		require.Len(t, vpnRoutes.Routes, 1)
		assert.Equal(t, "0.0.0.0/0", aws.ToString(vpnRoutes.Routes[0].DestinationCidr))
	})
}

// TestEC2IPAMParitySDK drives the real aws-sdk-go-v2 EC2 client across the
// IPAM core lifecycle (IPAM + scopes + pools + provisioned CIDRs + allocations),
// proving the query-protocol XML round-trips.
func TestEC2IPAMParitySDK(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	ipam, err := client.CreateIpam(ctx, &ec2.CreateIpamInput{Description: aws.String("corp")})
	require.NoError(t, err)
	require.NotNil(t, ipam.Ipam)
	ipamID := aws.ToString(ipam.Ipam.IpamId)
	assert.NotEmpty(t, ipamID)
	// Creating an IPAM implicitly creates a public + private default scope.
	privScopeID := aws.ToString(ipam.Ipam.PrivateDefaultScopeId)
	assert.NotEmpty(t, aws.ToString(ipam.Ipam.PublicDefaultScopeId))
	assert.NotEmpty(t, privScopeID)
	assert.EqualValues(t, 2, aws.ToInt32(ipam.Ipam.ScopeCount))

	desc, err := client.DescribeIpams(ctx, &ec2.DescribeIpamsInput{IpamIds: []string{ipamID}})
	require.NoError(t, err)
	require.Len(t, desc.Ipams, 1)

	scope, err := client.CreateIpamScope(ctx, &ec2.CreateIpamScopeInput{
		IpamId: aws.String(ipamID), Description: aws.String("extra"),
	})
	require.NoError(t, err)
	assert.False(t, aws.ToBool(scope.IpamScope.IsDefault))

	pool, err := client.CreateIpamPool(ctx, &ec2.CreateIpamPoolInput{
		IpamScopeId:   aws.String(privScopeID),
		AddressFamily: ec2types.AddressFamilyIpv4,
		Locale:        aws.String("us-east-1"),
	})
	require.NoError(t, err)
	poolID := aws.ToString(pool.IpamPool.IpamPoolId)
	assert.NotEmpty(t, poolID)
	assert.Equal(t, ec2types.AddressFamilyIpv4, pool.IpamPool.AddressFamily)

	// Provision supply into the pool, then read it back.
	_, err = client.ProvisionIpamPoolCidr(ctx, &ec2.ProvisionIpamPoolCidrInput{
		IpamPoolId: aws.String(poolID), Cidr: aws.String("10.0.0.0/16"),
	})
	require.NoError(t, err)

	cidrs, err := client.GetIpamPoolCidrs(ctx, &ec2.GetIpamPoolCidrsInput{IpamPoolId: aws.String(poolID)})
	require.NoError(t, err)
	require.Len(t, cidrs.IpamPoolCidrs, 1)
	assert.Equal(t, "10.0.0.0/16", aws.ToString(cidrs.IpamPoolCidrs[0].Cidr))

	// Allocate a CIDR out of the pool, then read + release it.
	alloc, err := client.AllocateIpamPoolCidr(ctx, &ec2.AllocateIpamPoolCidrInput{
		IpamPoolId: aws.String(poolID), Cidr: aws.String("10.0.1.0/24"),
	})
	require.NoError(t, err)
	allocID := aws.ToString(alloc.IpamPoolAllocation.IpamPoolAllocationId)
	assert.NotEmpty(t, allocID)

	allocs, err := client.GetIpamPoolAllocations(ctx, &ec2.GetIpamPoolAllocationsInput{IpamPoolId: aws.String(poolID)})
	require.NoError(t, err)
	require.Len(t, allocs.IpamPoolAllocations, 1)
	assert.Equal(t, "10.0.1.0/24", aws.ToString(allocs.IpamPoolAllocations[0].Cidr))

	rel, err := client.ReleaseIpamPoolAllocation(ctx, &ec2.ReleaseIpamPoolAllocationInput{
		IpamPoolId: aws.String(poolID), IpamPoolAllocationId: aws.String(allocID), Cidr: aws.String("10.0.1.0/24"),
	})
	require.NoError(t, err)
	assert.True(t, aws.ToBool(rel.Success))

	// Teardown in dependency order: pool (deprovision first) → scope → ipam.
	_, err = client.DeprovisionIpamPoolCidr(ctx, &ec2.DeprovisionIpamPoolCidrInput{
		IpamPoolId: aws.String(poolID), Cidr: aws.String("10.0.0.0/16"),
	})
	require.NoError(t, err)

	_, err = client.DeleteIpamPool(ctx, &ec2.DeleteIpamPoolInput{IpamPoolId: aws.String(poolID)})
	require.NoError(t, err)

	_, err = client.DeleteIpamScope(ctx, &ec2.DeleteIpamScopeInput{IpamScopeId: scope.IpamScope.IpamScopeId})
	require.NoError(t, err)

	_, err = client.DeleteIpam(ctx, &ec2.DeleteIpamInput{IpamId: aws.String(ipamID)})
	require.NoError(t, err)

	// Error path: describing a deleted IPAM by id returns an empty set.
	after, err := client.DescribeIpams(ctx, &ec2.DescribeIpamsInput{IpamIds: []string{ipamID}})
	require.NoError(t, err)
	assert.Empty(t, after.Ipams)
}

// TestEC2IPAMFullSDK drives the real EC2 client across the full IPAM surface
// beyond the core lifecycle: resource CIDRs + history, resource discovery +
// discovered getters, BYOASN + BYOIP, prefix-list resolver + targets,
// verification tokens, and policy + org-admin — proving the query wire.
func TestEC2IPAMFullSDK(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	// Prerequisite VPC + subnet so resource CIDRs / discovery / utilization
	// have something to report.
	vpcOut, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	require.NoError(t, err)
	vpcID := aws.ToString(vpcOut.Vpc.VpcId)
	_, err = client.CreateSubnet(ctx, &ec2.CreateSubnetInput{VpcId: aws.String(vpcID), CidrBlock: aws.String("10.0.1.0/24")})
	require.NoError(t, err)

	ipam, err := client.CreateIpam(ctx, &ec2.CreateIpamInput{})
	require.NoError(t, err)
	ipamID := aws.ToString(ipam.Ipam.IpamId)
	privScopeID := aws.ToString(ipam.Ipam.PrivateDefaultScopeId)
	// A default resource discovery + association is created with the IPAM.
	rdID := aws.ToString(ipam.Ipam.DefaultResourceDiscoveryId)
	require.NotEmpty(t, rdID)

	t.Run("resource cidrs + history", func(t *testing.T) {
		rc, err := client.GetIpamResourceCidrs(ctx, &ec2.GetIpamResourceCidrsInput{IpamScopeId: aws.String(privScopeID)})
		require.NoError(t, err)
		require.NotEmpty(t, rc.IpamResourceCidrs)

		hist, err := client.GetIpamAddressHistory(ctx, &ec2.GetIpamAddressHistoryInput{
			Cidr: aws.String("10.0.0.0/16"), IpamScopeId: aws.String(privScopeID),
		})
		require.NoError(t, err)
		require.NotEmpty(t, hist.HistoryRecords)
	})

	t.Run("resource discovery", func(t *testing.T) {
		descRD, err := client.DescribeIpamResourceDiscoveries(ctx, &ec2.DescribeIpamResourceDiscoveriesInput{
			IpamResourceDiscoveryIds: []string{rdID},
		})
		require.NoError(t, err)
		require.Len(t, descRD.IpamResourceDiscoveries, 1)
		assert.True(t, aws.ToBool(descRD.IpamResourceDiscoveries[0].IsDefault))

		accts, err := client.GetIpamDiscoveredAccounts(ctx, &ec2.GetIpamDiscoveredAccountsInput{
			IpamResourceDiscoveryId: aws.String(rdID), DiscoveryRegion: aws.String("us-east-1"),
		})
		require.NoError(t, err)
		require.Len(t, accts.IpamDiscoveredAccounts, 1)

		cidrs, err := client.GetIpamDiscoveredResourceCidrs(ctx, &ec2.GetIpamDiscoveredResourceCidrsInput{
			IpamResourceDiscoveryId: aws.String(rdID), ResourceRegion: aws.String("us-east-1"),
		})
		require.NoError(t, err)
		require.NotEmpty(t, cidrs.IpamDiscoveredResourceCidrs)
	})

	t.Run("byoip + byoasn", func(t *testing.T) {
		_, err := client.ProvisionByoipCidr(ctx, &ec2.ProvisionByoipCidrInput{Cidr: aws.String("203.0.113.0/24")})
		require.NoError(t, err)

		byoip, err := client.DescribeByoipCidrs(ctx, &ec2.DescribeByoipCidrsInput{MaxResults: aws.Int32(10)})
		require.NoError(t, err)
		require.Len(t, byoip.ByoipCidrs, 1)

		_, err = client.ProvisionIpamByoasn(ctx, &ec2.ProvisionIpamByoasnInput{
			IpamId: aws.String(ipamID), Asn: aws.String("64512"),
			AsnAuthorizationContext: &ec2types.AsnAuthorizationContext{
				Message: aws.String("msg"), Signature: aws.String("sig"),
			},
		})
		require.NoError(t, err)

		asns, err := client.DescribeIpamByoasn(ctx, &ec2.DescribeIpamByoasnInput{})
		require.NoError(t, err)
		require.Len(t, asns.Byoasns, 1)
	})

	t.Run("prefix list resolver + token", func(t *testing.T) {
		res, err := client.CreateIpamPrefixListResolver(ctx, &ec2.CreateIpamPrefixListResolverInput{
			IpamId: aws.String(ipamID), AddressFamily: ec2types.AddressFamilyIpv4,
		})
		require.NoError(t, err)
		resID := aws.ToString(res.IpamPrefixListResolver.IpamPrefixListResolverId)
		assert.NotEmpty(t, resID)

		descRes, err := client.DescribeIpamPrefixListResolvers(ctx, &ec2.DescribeIpamPrefixListResolversInput{
			IpamPrefixListResolverIds: []string{resID},
		})
		require.NoError(t, err)
		require.Len(t, descRes.IpamPrefixListResolvers, 1)

		tok, err := client.CreateIpamExternalResourceVerificationToken(ctx, &ec2.CreateIpamExternalResourceVerificationTokenInput{
			IpamId: aws.String(ipamID),
		})
		require.NoError(t, err)
		assert.NotEmpty(t, aws.ToString(tok.IpamExternalResourceVerificationToken.IpamExternalResourceVerificationTokenId))
	})

	t.Run("policy + org admin", func(t *testing.T) {
		pol, err := client.CreateIpamPolicy(ctx, &ec2.CreateIpamPolicyInput{IpamId: aws.String(ipamID)})
		require.NoError(t, err)
		polID := aws.ToString(pol.IpamPolicy.IpamPolicyId)
		assert.NotEmpty(t, polID)

		_, err = client.EnableIpamPolicy(ctx, &ec2.EnableIpamPolicyInput{IpamPolicyId: aws.String(polID)})
		require.NoError(t, err)

		enabled, err := client.GetEnabledIpamPolicy(ctx, &ec2.GetEnabledIpamPolicyInput{})
		require.NoError(t, err)
		assert.True(t, aws.ToBool(enabled.IpamPolicyEnabled))

		admin, err := client.EnableIpamOrganizationAdminAccount(ctx, &ec2.EnableIpamOrganizationAdminAccountInput{
			DelegatedAdminAccountId: aws.String("111122223333"),
		})
		require.NoError(t, err)
		assert.True(t, aws.ToBool(admin.Success))
	})
}

// TestIPAMMetricsSDK proves the derived AWS/IPAM metrics surface through the
// real CloudWatch SDK (ListMetrics + GetMetricStatistics), wired via the
// VPC driver's optional IPAMMetrics capability.
func TestIPAMMetricsSDK(t *testing.T) {
	provider := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{EC2: provider.EC2, VPC: provider.VPC, CloudWatch: provider.CloudWatch})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("t", "t", "")))
	require.NoError(t, err)

	ec2c := ec2.NewFromConfig(cfg, func(o *ec2.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	cw := cloudwatch.NewFromConfig(cfg, func(o *cloudwatch.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ctx := context.Background()

	// Build IPAM state: a VPC with a subnet (drives VpcIPUsage) and an IPAM.
	vpcOut, err := ec2c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	require.NoError(t, err)
	_, err = ec2c.CreateSubnet(ctx, &ec2.CreateSubnetInput{
		VpcId: vpcOut.Vpc.VpcId, CidrBlock: aws.String("10.0.0.0/24"),
	})
	require.NoError(t, err)
	_, err = ec2c.CreateIpam(ctx, &ec2.CreateIpamInput{})
	require.NoError(t, err)

	// ListMetrics on AWS/IPAM returns the derived metric set.
	list, err := cw.ListMetrics(ctx, &cloudwatch.ListMetricsInput{Namespace: aws.String("AWS/IPAM")})
	require.NoError(t, err)
	names := map[string]bool{}
	for _, m := range list.Metrics {
		names[aws.ToString(m.MetricName)] = true
	}
	assert.True(t, names["TotalActiveIpCount"], "expected TotalActiveIpCount metric")
	assert.True(t, names["VpcIPUsage"], "expected VpcIPUsage metric")

	// Regression (#318 review): a real metric plus an empty-namespace
	// "list all" call must return BOTH the real namespace and AWS/IPAM — the
	// IPAM shortcut must not drop every non-IPAM metric.
	_, err = cw.PutMetricData(ctx, &cloudwatch.PutMetricDataInput{
		Namespace:  aws.String("MyApp"),
		MetricData: []cwtypes.MetricDatum{{MetricName: aws.String("RequestCount"), Value: aws.Float64(1)}},
	})
	require.NoError(t, err)

	all, err := cw.ListMetrics(ctx, &cloudwatch.ListMetricsInput{})
	require.NoError(t, err)

	namespaces := map[string]bool{}
	for _, m := range all.Metrics {
		namespaces[aws.ToString(m.Namespace)] = true
	}
	assert.True(t, namespaces["MyApp"], "empty-namespace ListMetrics dropped the real MyApp metric")
	assert.True(t, namespaces["AWS/IPAM"], "empty-namespace ListMetrics dropped the AWS/IPAM metrics")

	// GetMetricStatistics for VpcIPUsage returns the computed utilization (a
	// /24 subnet in a /16 VPC = 256/65536 = ~0.39%).
	vpcID := aws.ToString(vpcOut.Vpc.VpcId)
	end := time.Now().UTC()
	stat, err := cw.GetMetricStatistics(ctx, &cloudwatch.GetMetricStatisticsInput{
		Namespace:  aws.String("AWS/IPAM"),
		MetricName: aws.String("VpcIPUsage"),
		Dimensions: []cwtypes.Dimension{
			{Name: aws.String("VpcID"), Value: aws.String(vpcID)},
			{Name: aws.String("AddressFamily"), Value: aws.String("IPv4")},
			{Name: aws.String("Region"), Value: aws.String("us-east-1")},
		},
		StartTime:  aws.Time(end.Add(-time.Hour)),
		EndTime:    aws.Time(end),
		Period:     aws.Int32(60),
		Statistics: []cwtypes.Statistic{cwtypes.StatisticAverage},
	})
	require.NoError(t, err)
	require.Len(t, stat.Datapoints, 1)
	assert.InDelta(t, 0.39, aws.ToFloat64(stat.Datapoints[0].Average), 0.05)
}
