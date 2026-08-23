package ec2_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newRoutingEdgeEC2 wires a full AWS server and returns an EC2 client pointed at
// it, for the routing-edge (route table / NAT / peering / ACL / IGW / prefix
// list) round-trip tests.
func newRoutingEdgeEC2(t *testing.T) *ec2.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	cfg.BaseEndpoint = aws.String(ts.URL)

	return ec2.NewFromConfig(cfg)
}

// mkVPCSubnet creates a VPC + subnet and returns their ids.
func mkVPCSubnet(t *testing.T, c *ec2.Client) (vpcID, subnetID string) {
	t.Helper()

	ctx := context.Background()

	vpc, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	vpcID = aws.ToString(vpc.Vpc.VpcId)

	sub, err := c.CreateSubnet(ctx, &ec2.CreateSubnetInput{
		VpcId:     aws.String(vpcID),
		CidrBlock: aws.String("10.0.1.0/24"),
	})
	if err != nil {
		t.Fatalf("CreateSubnet: %v", err)
	}

	return vpcID, aws.ToString(sub.Subnet.SubnetId)
}

// TestNatGatewayAddressSetRoundTrip pins that a public NAT gateway reports its
// address set (allocationId, networkInterfaceId, privateIp, publicIp) and
// connectivityType, both on create and describe. Terraform's aws_nat_gateway
// reads network_interface_id and public_ip off this set; without it those
// attributes come back empty and the resource never converges.
func TestNatGatewayAddressSetRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	_, subnetID := mkVPCSubnet(t, c)

	created, err := c.CreateNatGateway(ctx, &ec2.CreateNatGatewayInput{
		SubnetId:     aws.String(subnetID),
		AllocationId: aws.String("eipalloc-12345678"),
	})
	if err != nil {
		t.Fatalf("CreateNatGateway: %v", err)
	}

	natID := aws.ToString(created.NatGateway.NatGatewayId)
	assertNatAddresses(t, "create", created.NatGateway)

	desc, err := c.DescribeNatGateways(ctx, &ec2.DescribeNatGatewaysInput{
		NatGatewayIds: []string{natID},
	})
	if err != nil {
		t.Fatalf("DescribeNatGateways: %v", err)
	}

	if len(desc.NatGateways) != 1 {
		t.Fatalf("DescribeNatGateways returned %d, want 1", len(desc.NatGateways))
	}

	assertNatAddresses(t, "describe", &desc.NatGateways[0])
}

func assertNatAddresses(t *testing.T, phase string, n *ec2types.NatGateway) {
	t.Helper()

	if got := string(n.ConnectivityType); got != "public" {
		t.Errorf("%s: connectivityType = %q, want public", phase, got)
	}

	if len(n.NatGatewayAddresses) != 1 {
		t.Fatalf("%s: NatGatewayAddresses = %d, want 1", phase, len(n.NatGatewayAddresses))
	}

	addr := n.NatGatewayAddresses[0]
	if aws.ToString(addr.AllocationId) != "eipalloc-12345678" {
		t.Errorf("%s: allocationId = %q, want eipalloc-12345678", phase, aws.ToString(addr.AllocationId))
	}

	if aws.ToString(addr.NetworkInterfaceId) == "" {
		t.Errorf("%s: networkInterfaceId is empty", phase)
	}

	if aws.ToString(addr.PublicIp) == "" {
		t.Errorf("%s: publicIp is empty", phase)
	}

	if aws.ToString(addr.PrivateIp) == "" {
		t.Errorf("%s: privateIp is empty", phase)
	}
}

// TestNatGatewayPrivateConnectivity pins that a private NAT gateway echoes
// connectivityType=private and carries no public/Elastic IP.
func TestNatGatewayPrivateConnectivity(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	_, subnetID := mkVPCSubnet(t, c)

	created, err := c.CreateNatGateway(ctx, &ec2.CreateNatGatewayInput{
		SubnetId:         aws.String(subnetID),
		ConnectivityType: ec2types.ConnectivityTypePrivate,
	})
	if err != nil {
		t.Fatalf("CreateNatGateway: %v", err)
	}

	if got := string(created.NatGateway.ConnectivityType); got != "private" {
		t.Errorf("connectivityType = %q, want private", got)
	}

	if len(created.NatGateway.NatGatewayAddresses) != 1 {
		t.Fatalf("NatGatewayAddresses = %d, want 1", len(created.NatGateway.NatGatewayAddresses))
	}

	if ip := aws.ToString(created.NatGateway.NatGatewayAddresses[0].PublicIp); ip != "" {
		t.Errorf("private NAT gateway publicIp = %q, want empty", ip)
	}

	if aws.ToString(created.NatGateway.NatGatewayAddresses[0].PrivateIp) == "" {
		t.Error("private NAT gateway privateIp is empty")
	}
}
