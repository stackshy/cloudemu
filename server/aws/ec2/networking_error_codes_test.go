package ec2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// mkAttachedIGW creates an internet gateway attached to vpcID and returns its id.
func mkAttachedIGW(t *testing.T, c *ec2.Client, vpcID string) string {
	t.Helper()
	ctx := context.Background()

	igw, err := c.CreateInternetGateway(ctx, &ec2.CreateInternetGatewayInput{})
	if err != nil {
		t.Fatalf("CreateInternetGateway: %v", err)
	}

	id := aws.ToString(igw.InternetGateway.InternetGatewayId)

	if _, err := c.AttachInternetGateway(ctx, &ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String(id), VpcId: aws.String(vpcID),
	}); err != nil {
		t.Fatalf("AttachInternetGateway: %v", err)
	}

	return id
}

// mkRouteTable creates a route table in vpcID and returns its id.
func mkRouteTable(t *testing.T, c *ec2.Client, vpcID string) string {
	t.Helper()

	rt, err := c.CreateRouteTable(context.Background(), &ec2.CreateRouteTableInput{
		VpcId: aws.String(vpcID),
	})
	if err != nil {
		t.Fatalf("CreateRouteTable: %v", err)
	}

	return aws.ToString(rt.RouteTable.RouteTableId)
}

// TestCreateRouteBadGatewayCode pins that a route pointing at a nonexistent
// internet gateway is rejected with InvalidInternetGatewayID.NotFound rather than
// silently stored.
func TestCreateRouteBadGatewayCode(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	vpcID, _ := mkVPCSubnet(t, c)
	rtID := mkRouteTable(t, c, vpcID)

	_, err := c.CreateRoute(ctx, &ec2.CreateRouteInput{
		RouteTableId:         aws.String(rtID),
		DestinationCidrBlock: aws.String("0.0.0.0/0"),
		GatewayId:            aws.String("igw-bogus00"),
	})
	if err == nil {
		t.Fatal("CreateRoute(bad gateway) succeeded, want an error")
	}

	if got := apiCode(t, err); got != "InvalidInternetGatewayID.NotFound" {
		t.Errorf("code = %q, want InvalidInternetGatewayID.NotFound", got)
	}
}

// TestCreateRouteBadNatGatewayCode pins the NAT equivalent.
func TestCreateRouteBadNatGatewayCode(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	vpcID, _ := mkVPCSubnet(t, c)
	rtID := mkRouteTable(t, c, vpcID)

	_, err := c.CreateRoute(ctx, &ec2.CreateRouteInput{
		RouteTableId:         aws.String(rtID),
		DestinationCidrBlock: aws.String("0.0.0.0/0"),
		NatGatewayId:         aws.String("nat-bogus00"),
	})
	if err == nil {
		t.Fatal("CreateRoute(bad NAT) succeeded, want an error")
	}

	if got := apiCode(t, err); got != "InvalidNatGatewayID.NotFound" {
		t.Errorf("code = %q, want InvalidNatGatewayID.NotFound", got)
	}
}

// TestCreateRouteBadPeeringCode pins the peering equivalent.
func TestCreateRouteBadPeeringCode(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	vpcID, _ := mkVPCSubnet(t, c)
	rtID := mkRouteTable(t, c, vpcID)

	_, err := c.CreateRoute(ctx, &ec2.CreateRouteInput{
		RouteTableId:           aws.String(rtID),
		DestinationCidrBlock:   aws.String("10.9.0.0/16"),
		VpcPeeringConnectionId: aws.String("pcx-bogus00"),
	})
	if err == nil {
		t.Fatal("CreateRoute(bad peering) succeeded, want an error")
	}

	if got := apiCode(t, err); got != "InvalidVpcPeeringConnectionID.NotFound" {
		t.Errorf("code = %q, want InvalidVpcPeeringConnectionID.NotFound", got)
	}
}

// TestDuplicateRouteCode pins that a second route for the same destination CIDR
// answers RouteAlreadyExists, not the generic ResourceAlreadyExists.
func TestDuplicateRouteCode(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	vpcID, _ := mkVPCSubnet(t, c)
	igwID := mkAttachedIGW(t, c, vpcID)
	rtID := mkRouteTable(t, c, vpcID)

	if _, err := c.CreateRoute(ctx, &ec2.CreateRouteInput{
		RouteTableId:         aws.String(rtID),
		DestinationCidrBlock: aws.String("0.0.0.0/0"),
		GatewayId:            aws.String(igwID),
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}

	_, err := c.CreateRoute(ctx, &ec2.CreateRouteInput{
		RouteTableId:         aws.String(rtID),
		DestinationCidrBlock: aws.String("0.0.0.0/0"),
		GatewayId:            aws.String(igwID),
	})
	if err == nil {
		t.Fatal("duplicate CreateRoute succeeded, want an error")
	}

	if got := apiCode(t, err); got != "RouteAlreadyExists" {
		t.Errorf("code = %q, want RouteAlreadyExists", got)
	}
}

// TestDeleteMissingRouteCode pins that deleting a route that does not exist on an
// existing route table answers InvalidRoute.NotFound — not the route-table code,
// which would falsely imply the table itself is missing.
func TestDeleteMissingRouteCode(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	vpcID, _ := mkVPCSubnet(t, c)
	rtID := mkRouteTable(t, c, vpcID)

	_, err := c.DeleteRoute(ctx, &ec2.DeleteRouteInput{
		RouteTableId:         aws.String(rtID),
		DestinationCidrBlock: aws.String("172.16.0.0/16"),
	})
	if err == nil {
		t.Fatal("DeleteRoute(missing) succeeded, want an error")
	}

	if got := apiCode(t, err); got != "InvalidRoute.NotFound" {
		t.Errorf("code = %q, want InvalidRoute.NotFound", got)
	}
}

// TestSubnetOutOfVPCRangeCode pins that a subnet CIDR outside the VPC CIDR block
// is rejected with InvalidSubnet.Range rather than silently created.
func TestSubnetOutOfVPCRangeCode(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	vpcID, _ := mkVPCSubnet(t, c) // 10.0.0.0/16

	_, err := c.CreateSubnet(ctx, &ec2.CreateSubnetInput{
		VpcId:     aws.String(vpcID),
		CidrBlock: aws.String("192.168.5.0/24"),
	})
	if err == nil {
		t.Fatal("CreateSubnet(out-of-range CIDR) succeeded, want an error")
	}

	if got := apiCode(t, err); got != "InvalidSubnet.Range" {
		t.Errorf("code = %q, want InvalidSubnet.Range", got)
	}
}

// TestReleaseBogusAllocationCode pins that releasing an unknown allocation id
// answers InvalidAllocationID.NotFound — not the unrelated InvalidVpcID.NotFound.
func TestReleaseBogusAllocationCode(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	_, err := c.ReleaseAddress(ctx, &ec2.ReleaseAddressInput{
		AllocationId: aws.String("eipalloc-bogus00"),
	})
	if err == nil {
		t.Fatal("ReleaseAddress(bogus) succeeded, want an error")
	}

	if got := apiCode(t, err); got != "InvalidAllocationID.NotFound" {
		t.Errorf("code = %q, want InvalidAllocationID.NotFound", got)
	}
}

// TestNatGatewayBadSubnetCode pins that a NAT gateway in a nonexistent subnet
// answers InvalidSubnetID.NotFound — the subnet is what's missing, not the NAT.
func TestNatGatewayBadSubnetCode(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	_, err := c.CreateNatGateway(ctx, &ec2.CreateNatGatewayInput{
		SubnetId: aws.String("subnet-bogus00"),
	})
	if err == nil {
		t.Fatal("CreateNatGateway(bogus subnet) succeeded, want an error")
	}

	if got := apiCode(t, err); got != "InvalidSubnetID.NotFound" {
		t.Errorf("code = %q, want InvalidSubnetID.NotFound", got)
	}
}

// TestPeeringNonPendingAcceptCode pins that accepting a peering that is not in
// pending-acceptance state answers InvalidStateTransition, not DependencyViolation.
func TestPeeringNonPendingAcceptCode(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	vpcID, _ := mkVPCSubnet(t, c) // 10.0.0.0/16

	peer, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.1.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc(peer): %v", err)
	}

	peerID := aws.ToString(peer.Vpc.VpcId)

	created, err := c.CreateVpcPeeringConnection(ctx, &ec2.CreateVpcPeeringConnectionInput{
		VpcId: aws.String(vpcID), PeerVpcId: aws.String(peerID),
	})
	if err != nil {
		t.Fatalf("CreateVpcPeeringConnection: %v", err)
	}

	pcxID := aws.ToString(created.VpcPeeringConnection.VpcPeeringConnectionId)

	if _, err := c.AcceptVpcPeeringConnection(ctx, &ec2.AcceptVpcPeeringConnectionInput{
		VpcPeeringConnectionId: aws.String(pcxID),
	}); err != nil {
		t.Fatalf("AcceptVpcPeeringConnection: %v", err)
	}

	// Accepting an already-active peering is the illegal transition.
	_, err = c.AcceptVpcPeeringConnection(ctx, &ec2.AcceptVpcPeeringConnectionInput{
		VpcPeeringConnectionId: aws.String(pcxID),
	})
	if err == nil {
		t.Fatal("accepting an active peering succeeded, want an error")
	}

	if got := apiCode(t, err); got != "InvalidStateTransition" {
		t.Errorf("code = %q, want InvalidStateTransition", got)
	}
}
