package aws_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

		desc, err := client.DescribeVpnConnections(ctx, &ec2.DescribeVpnConnectionsInput{})
		require.NoError(t, err)
		assert.Len(t, desc.VpnConnections, 1)
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
		assert.NotEmpty(t, aws.ToString(out.ServiceConfiguration.ServiceId))
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
	})
}
