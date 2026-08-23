package ec2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// TestDescribeVpcsCIDRFilterNarrows pins that DescribeVpcs honors the cidr filter,
// returning only the matching VPC instead of every VPC. This is the Terraform /
// CLI data-source lookup the audit flagged as broken.
func TestDescribeVpcsCIDRFilterNarrows(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	vpcA := mkVPC(ctx, t, c, "10.0.0.0/16")
	mkVPC(ctx, t, c, "10.1.0.0/16")

	out, err := c.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{
		Filters: []ec2types.Filter{{Name: aws.String("cidr"), Values: []string{"10.0.0.0/16"}}},
	})
	if err != nil {
		t.Fatalf("DescribeVpcs: %v", err)
	}

	if len(out.Vpcs) != 1 || aws.ToString(out.Vpcs[0].VpcId) != vpcA {
		t.Fatalf("cidr filter returned %d vpcs, want only %s", len(out.Vpcs), vpcA)
	}

	if got := aws.ToString(out.Vpcs[0].OwnerId); got != "123456789012" {
		t.Errorf("VPC OwnerId = %q, want 123456789012", got)
	}
}

// TestDescribeVpcsTagFilter pins the tag:<key> filter on DescribeVpcs.
func TestDescribeVpcsTagFilter(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	tagged, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{
		CidrBlock: aws.String("10.2.0.0/16"),
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeVpc,
			Tags:         []ec2types.Tag{{Key: aws.String("Env"), Value: aws.String("prod")}},
		}},
	})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	mkVPC(ctx, t, c, "10.3.0.0/16")

	out, err := c.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{
		Filters: []ec2types.Filter{{Name: aws.String("tag:Env"), Values: []string{"prod"}}},
	})
	if err != nil {
		t.Fatalf("DescribeVpcs(tag): %v", err)
	}

	if len(out.Vpcs) != 1 || aws.ToString(out.Vpcs[0].VpcId) != aws.ToString(tagged.Vpc.VpcId) {
		t.Fatalf("tag filter returned %d vpcs, want only the tagged one", len(out.Vpcs))
	}
}

// TestAssociateDhcpOptionsReflectedOnVpc pins that AssociateDhcpOptions actually
// changes the VPC's dhcpOptionsId, rather than the VPC forever reporting
// "default".
func TestAssociateDhcpOptionsReflectedOnVpc(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	vpcID := mkVPC(ctx, t, c, "10.0.0.0/16")

	opts, err := c.CreateDhcpOptions(ctx, &ec2.CreateDhcpOptionsInput{
		DhcpConfigurations: []ec2types.NewDhcpConfiguration{{
			Key:    aws.String("domain-name-servers"),
			Values: []string{"10.0.0.2"},
		}},
	})
	if err != nil {
		t.Fatalf("CreateDhcpOptions: %v", err)
	}

	doptID := aws.ToString(opts.DhcpOptions.DhcpOptionsId)

	if _, err := c.AssociateDhcpOptions(ctx, &ec2.AssociateDhcpOptionsInput{
		DhcpOptionsId: aws.String(doptID),
		VpcId:         aws.String(vpcID),
	}); err != nil {
		t.Fatalf("AssociateDhcpOptions: %v", err)
	}

	out, err := c.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{VpcIds: []string{vpcID}})
	if err != nil {
		t.Fatalf("DescribeVpcs: %v", err)
	}

	if got := aws.ToString(out.Vpcs[0].DhcpOptionsId); got != doptID {
		t.Fatalf("VPC DhcpOptionsId = %q, want %q", got, doptID)
	}
}

// TestDescribeInternetGatewaysAttachmentFilter pins the attachment.vpc-id filter
// and the ownerId field on internet gateways.
func TestDescribeInternetGatewaysAttachmentFilter(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	vpcID := mkVPC(ctx, t, c, "10.0.0.0/16")

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

	// A second, unattached IGW must be filtered out by attachment.vpc-id.
	if _, err := c.CreateInternetGateway(ctx, &ec2.CreateInternetGatewayInput{}); err != nil {
		t.Fatalf("CreateInternetGateway(2): %v", err)
	}

	out, err := c.DescribeInternetGateways(ctx, &ec2.DescribeInternetGatewaysInput{
		Filters: []ec2types.Filter{{Name: aws.String("attachment.vpc-id"), Values: []string{vpcID}}},
	})
	if err != nil {
		t.Fatalf("DescribeInternetGateways: %v", err)
	}

	if len(out.InternetGateways) != 1 || aws.ToString(out.InternetGateways[0].InternetGatewayId) != igwID {
		t.Fatalf("attachment.vpc-id filter returned %d gateways, want only %s", len(out.InternetGateways), igwID)
	}

	if got := aws.ToString(out.InternetGateways[0].OwnerId); got != "123456789012" {
		t.Errorf("IGW OwnerId = %q, want 123456789012", got)
	}
}
