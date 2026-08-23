package ec2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
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

func mkPeeringVPC(t *testing.T, c *ec2.Client, cidr string) string {
	t.Helper()

	vpc, err := c.CreateVpc(context.Background(), &ec2.CreateVpcInput{CidrBlock: aws.String(cidr)})
	if err != nil {
		t.Fatalf("CreateVpc(%s): %v", cidr, err)
	}

	return aws.ToString(vpc.Vpc.VpcId)
}
