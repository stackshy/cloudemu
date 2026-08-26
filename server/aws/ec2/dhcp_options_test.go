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

// TestDescribeDhcpOptionsFilters pins that the key / value / dhcp-options-id
// filters narrow the result to the matching option sets instead of returning
// every set, that a non-matching filter yields an empty set, and that an explicit
// id list still resolves.
func TestDescribeDhcpOptionsFilters(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	optA := mkDhcpOptions(ctx, t, client, "domain-name-servers", "10.0.0.2")
	optB := mkDhcpOptions(ctx, t, client, "domain-name", "example.internal")

	byKey, err := client.DescribeDhcpOptions(ctx, &ec2.DescribeDhcpOptionsInput{
		Filters: []ec2types.Filter{{Name: aws.String("key"), Values: []string{"domain-name-servers"}}},
	})
	if err != nil {
		t.Fatalf("DescribeDhcpOptions(key): %v", err)
	}

	if len(byKey.DhcpOptions) != 1 || aws.ToString(byKey.DhcpOptions[0].DhcpOptionsId) != optA {
		t.Fatalf("key filter = %d option sets, want only %s", len(byKey.DhcpOptions), optA)
	}

	byValue, err := client.DescribeDhcpOptions(ctx, &ec2.DescribeDhcpOptionsInput{
		Filters: []ec2types.Filter{{Name: aws.String("value"), Values: []string{"example.internal"}}},
	})
	if err != nil {
		t.Fatalf("DescribeDhcpOptions(value): %v", err)
	}

	if len(byValue.DhcpOptions) != 1 || aws.ToString(byValue.DhcpOptions[0].DhcpOptionsId) != optB {
		t.Fatalf("value filter = %d option sets, want only %s", len(byValue.DhcpOptions), optB)
	}

	byID, err := client.DescribeDhcpOptions(ctx, &ec2.DescribeDhcpOptionsInput{
		Filters: []ec2types.Filter{{Name: aws.String("dhcp-options-id"), Values: []string{optA}}},
	})
	if err != nil {
		t.Fatalf("DescribeDhcpOptions(dhcp-options-id): %v", err)
	}

	if len(byID.DhcpOptions) != 1 || aws.ToString(byID.DhcpOptions[0].DhcpOptionsId) != optA {
		t.Fatalf("dhcp-options-id filter = %d option sets, want only %s", len(byID.DhcpOptions), optA)
	}

	none, err := client.DescribeDhcpOptions(ctx, &ec2.DescribeDhcpOptionsInput{
		Filters: []ec2types.Filter{{Name: aws.String("key"), Values: []string{"ntp-servers"}}},
	})
	if err != nil {
		t.Fatalf("DescribeDhcpOptions(bogus): %v", err)
	}

	if len(none.DhcpOptions) != 0 {
		t.Fatalf("non-matching filter returned %d option sets, want 0", len(none.DhcpOptions))
	}

	byList, err := client.DescribeDhcpOptions(ctx, &ec2.DescribeDhcpOptionsInput{DhcpOptionsIds: []string{optB}})
	if err != nil {
		t.Fatalf("DescribeDhcpOptions(id list): %v", err)
	}

	if len(byList.DhcpOptions) != 1 || aws.ToString(byList.DhcpOptions[0].DhcpOptionsId) != optB {
		t.Fatalf("id list = %d option sets, want only %s", len(byList.DhcpOptions), optB)
	}
}

// mkDhcpOptions creates a DHCP option set with one key/value and returns its id.
func mkDhcpOptions(ctx context.Context, t *testing.T, c *ec2.Client, key, value string) string {
	t.Helper()

	out, err := c.CreateDhcpOptions(ctx, &ec2.CreateDhcpOptionsInput{
		DhcpConfigurations: []ec2types.NewDhcpConfiguration{{
			Key:    aws.String(key),
			Values: []string{value},
		}},
	})
	if err != nil {
		t.Fatalf("CreateDhcpOptions(%s): %v", key, err)
	}

	return aws.ToString(out.DhcpOptions.DhcpOptionsId)
}
