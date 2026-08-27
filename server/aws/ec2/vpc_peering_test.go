package ec2_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"
)

// TestVpcPeeringVpcInfoRoundTrip pins that DescribeVpcPeeringConnections fills
// the requester/accepter VpcInfo (ownerId, cidrBlock, region) and a status
// message. Terraform's aws_vpc_peering_connection reads accepter/requester CIDR
// and owner id off these blocks; empty values leave the resource unable to
// compute its cross-account/cross-region attributes.
func TestVpcPeeringVpcInfoRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	reqVPC := mkPeeringVPC(t, c, "10.0.0.0/16")
	accVPC := mkPeeringVPC(t, c, "10.1.0.0/16")

	created, err := c.CreateVpcPeeringConnection(ctx, &ec2.CreateVpcPeeringConnectionInput{
		VpcId:     aws.String(reqVPC),
		PeerVpcId: aws.String(accVPC),
	})
	if err != nil {
		t.Fatalf("CreateVpcPeeringConnection: %v", err)
	}

	pcxID := aws.ToString(created.VpcPeeringConnection.VpcPeeringConnectionId)

	desc, err := c.DescribeVpcPeeringConnections(ctx, &ec2.DescribeVpcPeeringConnectionsInput{
		VpcPeeringConnectionIds: []string{pcxID},
	})
	if err != nil {
		t.Fatalf("DescribeVpcPeeringConnections: %v", err)
	}

	if len(desc.VpcPeeringConnections) != 1 {
		t.Fatalf("DescribeVpcPeeringConnections = %d, want 1", len(desc.VpcPeeringConnections))
	}

	pcx := desc.VpcPeeringConnections[0]

	req := pcx.RequesterVpcInfo
	if aws.ToString(req.VpcId) != reqVPC {
		t.Errorf("requester vpcId = %q, want %q", aws.ToString(req.VpcId), reqVPC)
	}

	if aws.ToString(req.CidrBlock) != "10.0.0.0/16" {
		t.Errorf("requester cidrBlock = %q, want 10.0.0.0/16", aws.ToString(req.CidrBlock))
	}

	if aws.ToString(req.OwnerId) == "" {
		t.Error("requester ownerId is empty")
	}

	if aws.ToString(req.Region) != "us-east-1" {
		t.Errorf("requester region = %q, want us-east-1", aws.ToString(req.Region))
	}

	if aws.ToString(pcx.AccepterVpcInfo.CidrBlock) != "10.1.0.0/16" {
		t.Errorf("accepter cidrBlock = %q, want 10.1.0.0/16", aws.ToString(pcx.AccepterVpcInfo.CidrBlock))
	}

	if aws.ToString(pcx.Status.Message) == "" {
		t.Errorf("status message is empty; code=%q", string(pcx.Status.Code))
	}
}

// TestRejectVpcPeeringConnection pins that the previously-undispatched
// RejectVpcPeeringConnection action reaches the driver and moves the connection
// to the rejected state.
func TestRejectVpcPeeringConnection(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	reqVPC := mkPeeringVPC(t, c, "10.0.0.0/16")
	accVPC := mkPeeringVPC(t, c, "10.2.0.0/16")

	created, err := c.CreateVpcPeeringConnection(ctx, &ec2.CreateVpcPeeringConnectionInput{
		VpcId:     aws.String(reqVPC),
		PeerVpcId: aws.String(accVPC),
	})
	if err != nil {
		t.Fatalf("CreateVpcPeeringConnection: %v", err)
	}

	pcxID := aws.ToString(created.VpcPeeringConnection.VpcPeeringConnectionId)

	if _, err := c.RejectVpcPeeringConnection(ctx, &ec2.RejectVpcPeeringConnectionInput{
		VpcPeeringConnectionId: aws.String(pcxID),
	}); err != nil {
		t.Fatalf("RejectVpcPeeringConnection: %v", err)
	}

	desc, err := c.DescribeVpcPeeringConnections(ctx, &ec2.DescribeVpcPeeringConnectionsInput{
		VpcPeeringConnectionIds: []string{pcxID},
	})
	if err != nil {
		t.Fatalf("DescribeVpcPeeringConnections: %v", err)
	}

	if len(desc.VpcPeeringConnections) != 1 {
		t.Fatalf("DescribeVpcPeeringConnections = %d, want 1", len(desc.VpcPeeringConnections))
	}

	if got := string(desc.VpcPeeringConnections[0].Status.Code); got != "rejected" {
		t.Errorf("status code = %q, want rejected", got)
	}
}

// TestDescribeVpcPeeringConnectionsFilters pins that the requester-vpc-info.vpc-id
// / status-code filters narrow the result instead of returning every connection,
// that a non-matching filter yields an empty set, and that an explicit id list
// still resolves.
func TestDescribeVpcPeeringConnectionsFilters(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	reqA := mkPeeringVPC(t, c, "10.0.0.0/16")
	accA := mkPeeringVPC(t, c, "10.1.0.0/16")
	reqB := mkPeeringVPC(t, c, "10.2.0.0/16")
	accB := mkPeeringVPC(t, c, "10.3.0.0/16")

	pcxA := mkPeering(ctx, t, c, reqA, accA)
	pcxB := mkPeering(ctx, t, c, reqB, accB)

	// Reject B so status-code distinguishes the two connections.
	if _, err := c.RejectVpcPeeringConnection(ctx, &ec2.RejectVpcPeeringConnectionInput{
		VpcPeeringConnectionId: aws.String(pcxB),
	}); err != nil {
		t.Fatalf("RejectVpcPeeringConnection: %v", err)
	}

	byReq, err := c.DescribeVpcPeeringConnections(ctx, &ec2.DescribeVpcPeeringConnectionsInput{
		Filters: []ec2types.Filter{{Name: aws.String("requester-vpc-info.vpc-id"), Values: []string{reqA}}},
	})
	if err != nil {
		t.Fatalf("DescribeVpcPeeringConnections(requester): %v", err)
	}

	if len(byReq.VpcPeeringConnections) != 1 ||
		aws.ToString(byReq.VpcPeeringConnections[0].VpcPeeringConnectionId) != pcxA {
		t.Fatalf("requester filter = %d connections, want only %s", len(byReq.VpcPeeringConnections), pcxA)
	}

	byStatus, err := c.DescribeVpcPeeringConnections(ctx, &ec2.DescribeVpcPeeringConnectionsInput{
		Filters: []ec2types.Filter{{Name: aws.String("status-code"), Values: []string{"rejected"}}},
	})
	if err != nil {
		t.Fatalf("DescribeVpcPeeringConnections(status-code): %v", err)
	}

	if len(byStatus.VpcPeeringConnections) != 1 ||
		aws.ToString(byStatus.VpcPeeringConnections[0].VpcPeeringConnectionId) != pcxB {
		t.Fatalf("status-code filter = %d connections, want only %s", len(byStatus.VpcPeeringConnections), pcxB)
	}

	none, err := c.DescribeVpcPeeringConnections(ctx, &ec2.DescribeVpcPeeringConnectionsInput{
		Filters: []ec2types.Filter{{Name: aws.String("status-code"), Values: []string{"deleted"}}},
	})
	if err != nil {
		t.Fatalf("DescribeVpcPeeringConnections(bogus): %v", err)
	}

	if len(none.VpcPeeringConnections) != 0 {
		t.Fatalf("non-matching filter returned %d connections, want 0", len(none.VpcPeeringConnections))
	}

	byList, err := c.DescribeVpcPeeringConnections(ctx, &ec2.DescribeVpcPeeringConnectionsInput{
		VpcPeeringConnectionIds: []string{pcxA},
	})
	if err != nil {
		t.Fatalf("DescribeVpcPeeringConnections(id list): %v", err)
	}

	if len(byList.VpcPeeringConnections) != 1 ||
		aws.ToString(byList.VpcPeeringConnections[0].VpcPeeringConnectionId) != pcxA {
		t.Fatalf("id list = %d connections, want only %s", len(byList.VpcPeeringConnections), pcxA)
	}
}

// mkPeering creates a peering connection and returns its id.
func mkPeering(ctx context.Context, t *testing.T, c *ec2.Client, reqVPC, accVPC string) string {
	t.Helper()

	out, err := c.CreateVpcPeeringConnection(ctx, &ec2.CreateVpcPeeringConnectionInput{
		VpcId:     aws.String(reqVPC),
		PeerVpcId: aws.String(accVPC),
	})
	if err != nil {
		t.Fatalf("CreateVpcPeeringConnection: %v", err)
	}

	return aws.ToString(out.VpcPeeringConnection.VpcPeeringConnectionId)
}

func mkPeeringVPC(t *testing.T, c *ec2.Client, cidr string) string {
	t.Helper()

	vpc, err := c.CreateVpc(context.Background(), &ec2.CreateVpcInput{CidrBlock: aws.String(cidr)})
	if err != nil {
		t.Fatalf("CreateVpc(%s): %v", cidr, err)
	}

	return aws.ToString(vpc.Vpc.VpcId)
}

// TestDescribeVpcPeeringConnectionsUnknownIDNotFound pins that an explicit,
// well-formed but non-existent pcx- id is InvalidVpcPeeringConnectionID.NotFound,
// matching the convention DescribeVpcs/DescribeVolumes already follow.
func TestDescribeVpcPeeringConnectionsUnknownIDNotFound(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	_, err := client.DescribeVpcPeeringConnections(ctx, &ec2.DescribeVpcPeeringConnectionsInput{
		VpcPeeringConnectionIds: []string{"pcx-00000000000000000"},
	})
	if err == nil {
		t.Fatal("DescribeVpcPeeringConnections(unknown) succeeded, want error")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidVpcPeeringConnectionID.NotFound" {
		t.Fatalf("error = %v, want InvalidVpcPeeringConnectionID.NotFound", err)
	}
}
