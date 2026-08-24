package aws_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/stretchr/testify/require"
)

// TestEC2DescribeUnknownIDNotFoundSDK proves that DescribeSubnets,
// DescribeRouteTables, DescribeInternetGateways, and DescribeNatGateways report
// the resource-specific NotFound error code when an explicit unknown ID is
// requested, matching real EC2 (and the sibling DescribeVpcs behavior).
func TestEC2DescribeUnknownIDNotFoundSDK(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	t.Run("subnets", func(t *testing.T) {
		_, err := client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
			SubnetIds: []string{"subnet-00000000000000000"},
		})
		assertAPIErrorCode(t, err, "InvalidSubnetID.NotFound")
	})

	t.Run("route tables", func(t *testing.T) {
		_, err := client.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{
			RouteTableIds: []string{"rtb-00000000000000000"},
		})
		assertAPIErrorCode(t, err, "InvalidRouteTableID.NotFound")
	})

	t.Run("internet gateways", func(t *testing.T) {
		_, err := client.DescribeInternetGateways(ctx, &ec2.DescribeInternetGatewaysInput{
			InternetGatewayIds: []string{"igw-00000000000000000"},
		})
		assertAPIErrorCode(t, err, "InvalidInternetGatewayID.NotFound")
	})

	t.Run("nat gateways", func(t *testing.T) {
		_, err := client.DescribeNatGateways(ctx, &ec2.DescribeNatGatewaysInput{
			NatGatewayIds: []string{"nat-00000000000000000"},
		})
		assertAPIErrorCode(t, err, "NatGatewayNotFound")
	})

	t.Run("known id still succeeds", func(t *testing.T) {
		vpcOut, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
		require.NoError(t, err)
		vpcID := aws.ToString(vpcOut.Vpc.VpcId)

		subnetOut, err := client.CreateSubnet(ctx, &ec2.CreateSubnetInput{
			VpcId: aws.String(vpcID), CidrBlock: aws.String("10.0.1.0/24"),
		})
		require.NoError(t, err)
		subnetID := aws.ToString(subnetOut.Subnet.SubnetId)

		got, err := client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
			SubnetIds: []string{subnetID},
		})
		require.NoError(t, err)
		require.Len(t, got.Subnets, 1)
		require.Equal(t, subnetID, aws.ToString(got.Subnets[0].SubnetId))
	})
}
