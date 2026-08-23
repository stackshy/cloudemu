package ec2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// TestInternetGatewayAttachmentAvailable pins that an attached internet gateway
// reports attachment state "available" (not "attached"). Terraform's
// aws_internet_gateway_attachment waiter blocks until it sees "available", so
// reporting "attached" hangs the attach until it times out.
func TestInternetGatewayAttachmentAvailable(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	vpc, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	vpcID := aws.ToString(vpc.Vpc.VpcId)

	igw, err := c.CreateInternetGateway(ctx, &ec2.CreateInternetGatewayInput{})
	if err != nil {
		t.Fatalf("CreateInternetGateway: %v", err)
	}

	igwID := aws.ToString(igw.InternetGateway.InternetGatewayId)

	if _, err := c.AttachInternetGateway(ctx, &ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String(vpcID),
	}); err != nil {
		t.Fatalf("AttachInternetGateway: %v", err)
	}

	desc, err := c.DescribeInternetGateways(ctx, &ec2.DescribeInternetGatewaysInput{
		InternetGatewayIds: []string{igwID},
	})
	if err != nil {
		t.Fatalf("DescribeInternetGateways: %v", err)
	}

	if len(desc.InternetGateways) != 1 {
		t.Fatalf("DescribeInternetGateways = %d, want 1", len(desc.InternetGateways))
	}

	atts := desc.InternetGateways[0].Attachments
	if len(atts) != 1 {
		t.Fatalf("attachments = %d, want 1", len(atts))
	}

	if got := string(atts[0].State); got != "available" {
		t.Errorf("attachment state = %q, want available", got)
	}

	if aws.ToString(atts[0].VpcId) != vpcID {
		t.Errorf("attachment vpcId = %q, want %q", aws.ToString(atts[0].VpcId), vpcID)
	}
}
