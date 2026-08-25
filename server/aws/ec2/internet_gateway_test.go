package ec2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
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

// TestInternetGatewayAttachmentStateFilter pins that filtering by the AWS-documented
// attachment.state value ("available") matches an attached gateway. The filter must
// use the same wire value the describe output reports, not the internal "attached".
func TestInternetGatewayAttachmentStateFilter(t *testing.T) {
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
		InternetGatewayId: aws.String(igwID), VpcId: aws.String(vpcID),
	}); err != nil {
		t.Fatalf("AttachInternetGateway: %v", err)
	}

	out, err := c.DescribeInternetGateways(ctx, &ec2.DescribeInternetGatewaysInput{
		Filters: []ec2types.Filter{{Name: aws.String("attachment.state"), Values: []string{"available"}}},
	})
	if err != nil {
		t.Fatalf("DescribeInternetGateways: %v", err)
	}

	if len(out.InternetGateways) != 1 || aws.ToString(out.InternetGateways[0].InternetGatewayId) != igwID {
		t.Fatalf("attachment.state=available returned %d gateways, want only %s", len(out.InternetGateways), igwID)
	}
}

// TestDescribeInternetGatewaysPaginatesAllOnce pins that
// DescribeInternetGateways honors MaxResults/NextToken, paging every gateway
// exactly once with no duplicates across pages.
func TestDescribeInternetGatewaysPaginatesAllOnce(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	want := map[string]int{}
	for range 3 {
		igw, err := c.CreateInternetGateway(ctx, &ec2.CreateInternetGatewayInput{})
		if err != nil {
			t.Fatalf("CreateInternetGateway: %v", err)
		}

		want[aws.ToString(igw.InternetGateway.InternetGatewayId)] = 0
	}

	seen := map[string]int{}

	var token *string

	for {
		out, err := c.DescribeInternetGateways(ctx, &ec2.DescribeInternetGatewaysInput{
			MaxResults: aws.Int32(1),
			NextToken:  token,
		})
		if err != nil {
			t.Fatalf("DescribeInternetGateways: %v", err)
		}

		if len(out.InternetGateways) > 1 {
			t.Fatalf("page returned %d gateways, want at most 1", len(out.InternetGateways))
		}

		for _, igw := range out.InternetGateways {
			seen[aws.ToString(igw.InternetGatewayId)]++
		}

		if aws.ToString(out.NextToken) == "" {
			break
		}

		token = out.NextToken
	}

	if len(seen) != len(want) {
		t.Fatalf("paged through %d gateways, want %d", len(seen), len(want))
	}

	for id, n := range seen {
		if n != 1 {
			t.Fatalf("gateway %s seen %d times across pages, want 1", id, n)
		}
	}
}

// TestOneInternetGatewayPerVPC pins the one-IGW-per-VPC invariant. Attaching a
// second, different internet gateway to a VPC that already has one must fail
// with Resource.AlreadyAssociated (real EC2: "Network vpc-… already has an
// internet gateway attached"); the first gateway stays attached. Attaching an
// already-attached gateway onto a second VPC is the same error code, not a
// DependencyViolation.
func TestOneInternetGatewayPerVPC(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	vpcA, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc A: %v", err)
	}

	vpcAID := aws.ToString(vpcA.Vpc.VpcId)

	vpcB, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.1.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc B: %v", err)
	}

	vpcBID := aws.ToString(vpcB.Vpc.VpcId)

	igwA := mkIGW(ctx, t, c)
	igwB := mkIGW(ctx, t, c)

	if _, err := c.AttachInternetGateway(ctx, &ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String(igwA), VpcId: aws.String(vpcAID),
	}); err != nil {
		t.Fatalf("AttachInternetGateway A: %v", err)
	}

	// A second, different gateway onto the same VPC -> Resource.AlreadyAssociated.
	_, err = c.AttachInternetGateway(ctx, &ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String(igwB), VpcId: aws.String(vpcAID),
	})
	if err == nil {
		t.Fatal("attaching a second IGW to the same VPC succeeded, want Resource.AlreadyAssociated")
	}

	if code := apiCode(t, err); code != "Resource.AlreadyAssociated" {
		t.Errorf("second-IGW error code = %q, want Resource.AlreadyAssociated", code)
	}

	// The first gateway is still the VPC's only attachment.
	desc, err := c.DescribeInternetGateways(ctx, &ec2.DescribeInternetGatewaysInput{
		Filters: []ec2types.Filter{{Name: aws.String("attachment.vpc-id"), Values: []string{vpcAID}}},
	})
	if err != nil {
		t.Fatalf("DescribeInternetGateways: %v", err)
	}

	if len(desc.InternetGateways) != 1 || aws.ToString(desc.InternetGateways[0].InternetGatewayId) != igwA {
		t.Fatalf("VPC attachments = %+v, want only %s", desc.InternetGateways, igwA)
	}

	// Attaching the already-attached igwA onto a second VPC -> same code.
	_, err = c.AttachInternetGateway(ctx, &ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String(igwA), VpcId: aws.String(vpcBID),
	})
	if err == nil {
		t.Fatal("attaching an already-attached IGW to a second VPC succeeded, want Resource.AlreadyAssociated")
	}

	if code := apiCode(t, err); code != "Resource.AlreadyAssociated" {
		t.Errorf("already-attached-IGW error code = %q, want Resource.AlreadyAssociated", code)
	}
}
