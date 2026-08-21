package aws

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestAWSNetworkingCompat drives a realistic VPC control-plane lifecycle
// through the real aws-sdk-go-v2 EC2 client and records one compat result per
// portable "networking" operation exercised. AWS folds VPC into the EC2
// service, so the wire handler is server/aws/ec2 (Drivers.VPC). Operation
// names match the portable Networking driver in docs/coverage/coverage.json
// (providers.aws = "VPC"): the SDK AuthorizeSecurityGroupIngress call maps to
// the "AddIngressRule" driver op, AuthorizeSecurityGroupEgress to
// "AddEgressRule", RevokeSecurityGroupIngress to "RemoveIngressRule",
// RevokeSecurityGroupEgress to "RemoveEgressRule", CreateNetworkAclEntry to
// "AddNetworkACLRule", and the Vpc/Acl/Nat SDK spellings to their VPC/ACL/NAT
// portable names.
func TestAWSNetworkingCompat(t *testing.T) {
	provider := cloudemu.NewAWS()
	sess := compat.BootAWS(t, awsserver.Drivers{EC2: provider.EC2, VPC: provider.VPC})

	client := awsec2.NewFromConfig(sess.Config(), func(o *awsec2.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})
	ctx := context.Background()

	const (
		svc         = "networking"
		vpcCIDR     = "10.0.0.0/16"
		subnetCIDR  = "10.0.1.0/24"
		routeCIDR   = "0.0.0.0/0"
		peerVPCCIDR = "10.1.0.0/16"
	)

	var (
		vpcID        string
		peerVPCID    string
		subnetID     string
		sgID         string
		igwID        string
		routeTableID string
		assocID      string
		naclID       string
		allocID      string
		natID        string
	)

	sess.Op(svc, "CreateVPC", func() error {
		out, err := client.CreateVpc(ctx, &awsec2.CreateVpcInput{
			CidrBlock: aws.String(vpcCIDR),
		})
		if err != nil {
			return err
		}

		vpcID = aws.ToString(out.Vpc.VpcId)
		if vpcID == "" {
			return fmt.Errorf("CreateVpc returned empty vpc id")
		}

		return nil
	})

	sess.Op(svc, "DescribeVPCs", func() error {
		out, err := client.DescribeVpcs(ctx, &awsec2.DescribeVpcsInput{
			VpcIds: []string{vpcID},
		})
		if err != nil {
			return err
		}

		if len(out.Vpcs) == 0 {
			return fmt.Errorf("DescribeVpcs did not return %q", vpcID)
		}

		return nil
	})

	sess.Op(svc, "CreateSubnet", func() error {
		out, err := client.CreateSubnet(ctx, &awsec2.CreateSubnetInput{
			VpcId:     aws.String(vpcID),
			CidrBlock: aws.String(subnetCIDR),
		})
		if err != nil {
			return err
		}

		subnetID = aws.ToString(out.Subnet.SubnetId)
		if subnetID == "" {
			return fmt.Errorf("CreateSubnet returned empty subnet id")
		}

		return nil
	})

	sess.Op(svc, "DescribeSubnets", func() error {
		out, err := client.DescribeSubnets(ctx, &awsec2.DescribeSubnetsInput{
			SubnetIds: []string{subnetID},
		})
		if err != nil {
			return err
		}

		if len(out.Subnets) == 0 {
			return fmt.Errorf("DescribeSubnets did not return %q", subnetID)
		}

		return nil
	})

	sess.Op(svc, "CreateSecurityGroup", func() error {
		out, err := client.CreateSecurityGroup(ctx, &awsec2.CreateSecurityGroupInput{
			GroupName:   aws.String("compat-sg"),
			Description: aws.String("compat security group"),
			VpcId:       aws.String(vpcID),
		})
		if err != nil {
			return err
		}

		sgID = aws.ToString(out.GroupId)
		if sgID == "" {
			return fmt.Errorf("CreateSecurityGroup returned empty group id")
		}

		return nil
	})

	sess.Op(svc, "DescribeSecurityGroups", func() error {
		out, err := client.DescribeSecurityGroups(ctx, &awsec2.DescribeSecurityGroupsInput{
			GroupIds: []string{sgID},
		})
		if err != nil {
			return err
		}

		if len(out.SecurityGroups) == 0 {
			return fmt.Errorf("DescribeSecurityGroups did not return %q", sgID)
		}

		return nil
	})

	ingress := []ec2types.IpPermission{{
		IpProtocol: aws.String("tcp"),
		FromPort:   aws.Int32(22),
		ToPort:     aws.Int32(22),
		IpRanges:   []ec2types.IpRange{{CidrIp: aws.String("0.0.0.0/0")}},
	}}

	egress := []ec2types.IpPermission{{
		IpProtocol: aws.String("tcp"),
		FromPort:   aws.Int32(443),
		ToPort:     aws.Int32(443),
		IpRanges:   []ec2types.IpRange{{CidrIp: aws.String("0.0.0.0/0")}},
	}}

	sess.Op(svc, "AddIngressRule", func() error {
		_, err := client.AuthorizeSecurityGroupIngress(ctx, &awsec2.AuthorizeSecurityGroupIngressInput{
			GroupId:       aws.String(sgID),
			IpPermissions: ingress,
		})

		return err
	})

	sess.Op(svc, "AddEgressRule", func() error {
		_, err := client.AuthorizeSecurityGroupEgress(ctx, &awsec2.AuthorizeSecurityGroupEgressInput{
			GroupId:       aws.String(sgID),
			IpPermissions: egress,
		})

		return err
	})

	sess.Op(svc, "CreateInternetGateway", func() error {
		out, err := client.CreateInternetGateway(ctx, &awsec2.CreateInternetGatewayInput{})
		if err != nil {
			return err
		}

		igwID = aws.ToString(out.InternetGateway.InternetGatewayId)
		if igwID == "" {
			return fmt.Errorf("CreateInternetGateway returned empty id")
		}

		return nil
	})

	sess.Op(svc, "AttachInternetGateway", func() error {
		_, err := client.AttachInternetGateway(ctx, &awsec2.AttachInternetGatewayInput{
			InternetGatewayId: aws.String(igwID),
			VpcId:             aws.String(vpcID),
		})

		return err
	})

	sess.Op(svc, "DescribeInternetGateways", func() error {
		out, err := client.DescribeInternetGateways(ctx, &awsec2.DescribeInternetGatewaysInput{
			InternetGatewayIds: []string{igwID},
		})
		if err != nil {
			return err
		}

		if len(out.InternetGateways) == 0 {
			return fmt.Errorf("DescribeInternetGateways did not return %q", igwID)
		}

		return nil
	})

	sess.Op(svc, "CreateRouteTable", func() error {
		out, err := client.CreateRouteTable(ctx, &awsec2.CreateRouteTableInput{
			VpcId: aws.String(vpcID),
		})
		if err != nil {
			return err
		}

		routeTableID = aws.ToString(out.RouteTable.RouteTableId)
		if routeTableID == "" {
			return fmt.Errorf("CreateRouteTable returned empty id")
		}

		return nil
	})

	sess.Op(svc, "CreateRoute", func() error {
		_, err := client.CreateRoute(ctx, &awsec2.CreateRouteInput{
			RouteTableId:         aws.String(routeTableID),
			DestinationCidrBlock: aws.String(routeCIDR),
			GatewayId:            aws.String(igwID),
		})

		return err
	})

	sess.Op(svc, "AssociateRouteTable", func() error {
		out, err := client.AssociateRouteTable(ctx, &awsec2.AssociateRouteTableInput{
			RouteTableId: aws.String(routeTableID),
			SubnetId:     aws.String(subnetID),
		})
		if err != nil {
			return err
		}

		assocID = aws.ToString(out.AssociationId)

		return nil
	})

	sess.Op(svc, "DescribeRouteTables", func() error {
		out, err := client.DescribeRouteTables(ctx, &awsec2.DescribeRouteTablesInput{
			RouteTableIds: []string{routeTableID},
		})
		if err != nil {
			return err
		}

		if len(out.RouteTables) == 0 {
			return fmt.Errorf("DescribeRouteTables did not return %q", routeTableID)
		}

		return nil
	})

	sess.Op(svc, "CreateNetworkACL", func() error {
		out, err := client.CreateNetworkAcl(ctx, &awsec2.CreateNetworkAclInput{
			VpcId: aws.String(vpcID),
		})
		if err != nil {
			return err
		}

		naclID = aws.ToString(out.NetworkAcl.NetworkAclId)
		if naclID == "" {
			return fmt.Errorf("CreateNetworkAcl returned empty id")
		}

		return nil
	})

	sess.Op(svc, "AddNetworkACLRule", func() error {
		_, err := client.CreateNetworkAclEntry(ctx, &awsec2.CreateNetworkAclEntryInput{
			NetworkAclId: aws.String(naclID),
			RuleNumber:   aws.Int32(100),
			Protocol:     aws.String("-1"),
			RuleAction:   ec2types.RuleActionAllow,
			Egress:       aws.Bool(false),
			CidrBlock:    aws.String("0.0.0.0/0"),
		})

		return err
	})

	sess.Op(svc, "DescribeNetworkACLs", func() error {
		out, err := client.DescribeNetworkAcls(ctx, &awsec2.DescribeNetworkAclsInput{
			NetworkAclIds: []string{naclID},
		})
		if err != nil {
			return err
		}

		if len(out.NetworkAcls) == 0 {
			return fmt.Errorf("DescribeNetworkAcls did not return %q", naclID)
		}

		return nil
	})

	sess.Op(svc, "CreatePeeringConnection", func() error {
		peer, err := client.CreateVpc(ctx, &awsec2.CreateVpcInput{
			CidrBlock: aws.String(peerVPCCIDR),
		})
		if err != nil {
			return err
		}

		peerVPCID = aws.ToString(peer.Vpc.VpcId)

		_, err = client.CreateVpcPeeringConnection(ctx, &awsec2.CreateVpcPeeringConnectionInput{
			VpcId:     aws.String(vpcID),
			PeerVpcId: aws.String(peerVPCID),
		})

		return err
	})

	sess.Op(svc, "DescribePeeringConnections", func() error {
		_, err := client.DescribeVpcPeeringConnections(ctx, &awsec2.DescribeVpcPeeringConnectionsInput{})

		return err
	})

	sess.Op(svc, "AllocateAddress", func() error {
		out, err := client.AllocateAddress(ctx, &awsec2.AllocateAddressInput{
			Domain: ec2types.DomainTypeVpc,
		})
		if err != nil {
			return err
		}

		allocID = aws.ToString(out.AllocationId)
		if allocID == "" {
			return fmt.Errorf("AllocateAddress returned empty allocation id")
		}

		return nil
	})

	sess.Op(svc, "DescribeAddresses", func() error {
		out, err := client.DescribeAddresses(ctx, &awsec2.DescribeAddressesInput{
			AllocationIds: []string{allocID},
		})
		if err != nil {
			return err
		}

		if len(out.Addresses) == 0 {
			return fmt.Errorf("DescribeAddresses did not return %q", allocID)
		}

		return nil
	})

	sess.Op(svc, "CreateNATGateway", func() error {
		out, err := client.CreateNatGateway(ctx, &awsec2.CreateNatGatewayInput{
			SubnetId:     aws.String(subnetID),
			AllocationId: aws.String(allocID),
		})
		if err != nil {
			return err
		}

		natID = aws.ToString(out.NatGateway.NatGatewayId)
		if natID == "" {
			return fmt.Errorf("CreateNatGateway returned empty id")
		}

		return nil
	})

	sess.Op(svc, "DescribeNATGateways", func() error {
		out, err := client.DescribeNatGateways(ctx, &awsec2.DescribeNatGatewaysInput{
			NatGatewayIds: []string{natID},
		})
		if err != nil {
			return err
		}

		if len(out.NatGateways) == 0 {
			return fmt.Errorf("DescribeNatGateways did not return %q", natID)
		}

		return nil
	})

	sess.Op(svc, "DeleteNATGateway", func() error {
		_, err := client.DeleteNatGateway(ctx, &awsec2.DeleteNatGatewayInput{
			NatGatewayId: aws.String(natID),
		})

		return err
	})

	sess.Op(svc, "ReleaseAddress", func() error {
		_, err := client.ReleaseAddress(ctx, &awsec2.ReleaseAddressInput{
			AllocationId: aws.String(allocID),
		})

		return err
	})

	sess.Op(svc, "RemoveNetworkACLRule", func() error {
		_, err := client.DeleteNetworkAclEntry(ctx, &awsec2.DeleteNetworkAclEntryInput{
			NetworkAclId: aws.String(naclID),
			RuleNumber:   aws.Int32(100),
			Egress:       aws.Bool(false),
		})

		return err
	})

	sess.Op(svc, "DeleteNetworkACL", func() error {
		_, err := client.DeleteNetworkAcl(ctx, &awsec2.DeleteNetworkAclInput{
			NetworkAclId: aws.String(naclID),
		})

		return err
	})

	sess.Op(svc, "DisassociateRouteTable", func() error {
		_, err := client.DisassociateRouteTable(ctx, &awsec2.DisassociateRouteTableInput{
			AssociationId: aws.String(assocID),
		})

		return err
	})

	sess.Op(svc, "DeleteRoute", func() error {
		_, err := client.DeleteRoute(ctx, &awsec2.DeleteRouteInput{
			RouteTableId:         aws.String(routeTableID),
			DestinationCidrBlock: aws.String(routeCIDR),
		})

		return err
	})

	sess.Op(svc, "DeleteRouteTable", func() error {
		_, err := client.DeleteRouteTable(ctx, &awsec2.DeleteRouteTableInput{
			RouteTableId: aws.String(routeTableID),
		})

		return err
	})

	sess.Op(svc, "DetachInternetGateway", func() error {
		_, err := client.DetachInternetGateway(ctx, &awsec2.DetachInternetGatewayInput{
			InternetGatewayId: aws.String(igwID),
			VpcId:             aws.String(vpcID),
		})

		return err
	})

	sess.Op(svc, "DeleteInternetGateway", func() error {
		_, err := client.DeleteInternetGateway(ctx, &awsec2.DeleteInternetGatewayInput{
			InternetGatewayId: aws.String(igwID),
		})

		return err
	})

	sess.Op(svc, "RemoveIngressRule", func() error {
		_, err := client.RevokeSecurityGroupIngress(ctx, &awsec2.RevokeSecurityGroupIngressInput{
			GroupId:       aws.String(sgID),
			IpPermissions: ingress,
		})

		return err
	})

	sess.Op(svc, "RemoveEgressRule", func() error {
		_, err := client.RevokeSecurityGroupEgress(ctx, &awsec2.RevokeSecurityGroupEgressInput{
			GroupId:       aws.String(sgID),
			IpPermissions: egress,
		})

		return err
	})

	sess.Op(svc, "DeleteSecurityGroup", func() error {
		_, err := client.DeleteSecurityGroup(ctx, &awsec2.DeleteSecurityGroupInput{
			GroupId: aws.String(sgID),
		})

		return err
	})

	sess.Op(svc, "DeleteSubnet", func() error {
		_, err := client.DeleteSubnet(ctx, &awsec2.DeleteSubnetInput{
			SubnetId: aws.String(subnetID),
		})

		return err
	})

	sess.Op(svc, "DeleteVPC", func() error {
		_, err := client.DeleteVpc(ctx, &awsec2.DeleteVpcInput{
			VpcId: aws.String(vpcID),
		})

		return err
	})
}
