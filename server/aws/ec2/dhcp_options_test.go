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

// TestDeleteDhcpOptionsInUseDependencyViolation pins that a DHCP option set
// still associated with a VPC cannot be deleted (DependencyViolation), and that
// re-associating the VPC with the default set frees it for deletion.
func TestDeleteDhcpOptionsInUseDependencyViolation(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	vpc, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}
	vpcID := aws.ToString(vpc.Vpc.VpcId)

	opts, err := client.CreateDhcpOptions(ctx, &ec2.CreateDhcpOptionsInput{
		DhcpConfigurations: []ec2types.NewDhcpConfiguration{{
			Key:    aws.String("domain-name-servers"),
			Values: []string{"10.0.0.2"},
		}},
	})
	if err != nil {
		t.Fatalf("CreateDhcpOptions: %v", err)
	}
	optID := aws.ToString(opts.DhcpOptions.DhcpOptionsId)

	if _, err := client.AssociateDhcpOptions(ctx, &ec2.AssociateDhcpOptionsInput{
		DhcpOptionsId: aws.String(optID),
		VpcId:         aws.String(vpcID),
	}); err != nil {
		t.Fatalf("AssociateDhcpOptions: %v", err)
	}

	_, err = client.DeleteDhcpOptions(ctx, &ec2.DeleteDhcpOptionsInput{DhcpOptionsId: aws.String(optID)})
	if err == nil {
		t.Fatal("DeleteDhcpOptions(in-use) succeeded, want DependencyViolation")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "DependencyViolation" {
		t.Fatalf("DeleteDhcpOptions(in-use) error = %v, want DependencyViolation", err)
	}

	// Re-associate the VPC with the Amazon-provided default set, then delete.
	if _, err := client.AssociateDhcpOptions(ctx, &ec2.AssociateDhcpOptionsInput{
		DhcpOptionsId: aws.String("default"),
		VpcId:         aws.String(vpcID),
	}); err != nil {
		t.Fatalf("AssociateDhcpOptions(default): %v", err)
	}

	if _, err := client.DeleteDhcpOptions(ctx, &ec2.DeleteDhcpOptionsInput{
		DhcpOptionsId: aws.String(optID),
	}); err != nil {
		t.Fatalf("DeleteDhcpOptions after re-associate: %v", err)
	}
}

// TestDescribeDhcpOptionsUnknownIDNotFound pins that an explicit, non-existent
// dopt- id is InvalidDhcpOptionID.NotFound.
func TestDescribeDhcpOptionsUnknownIDNotFound(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	_, err := client.DescribeDhcpOptions(ctx, &ec2.DescribeDhcpOptionsInput{
		DhcpOptionsIds: []string{"dopt-00000000000000000"},
	})
	if err == nil {
		t.Fatal("DescribeDhcpOptions(unknown) succeeded, want error")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidDhcpOptionID.NotFound" {
		t.Fatalf("error = %v, want InvalidDhcpOptionID.NotFound", err)
	}
}
