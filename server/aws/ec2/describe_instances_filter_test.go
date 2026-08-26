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

// TestDescribeInstancesFilterAppliesVPCAndSubnet pins that DescribeInstances
// applies the vpc-id and subnet-id filters, returning only the instance placed
// in that VPC/subnet rather than the whole fleet (the over-inclusive match-all
// bug for unmodeled filters).
func TestDescribeInstancesFilterAppliesVPCAndSubnet(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	vpc, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	vpcID := aws.ToString(vpc.Vpc.VpcId)

	subnet, err := c.CreateSubnet(ctx, &ec2.CreateSubnetInput{
		VpcId: aws.String(vpcID), CidrBlock: aws.String("10.0.1.0/24"),
	})
	if err != nil {
		t.Fatalf("CreateSubnet: %v", err)
	}

	subnetID := aws.ToString(subnet.Subnet.SubnetId)

	inVPC, err := c.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-123"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1), SubnetId: aws.String(subnetID),
	})
	if err != nil {
		t.Fatalf("RunInstances (in vpc): %v", err)
	}

	wantID := aws.ToString(inVPC.Instances[0].InstanceId)

	// A second instance launched with no subnet must NOT satisfy the filters.
	if _, err = c.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-456"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	}); err != nil {
		t.Fatalf("RunInstances (no vpc): %v", err)
	}

	for _, tc := range []struct {
		name, filterName, filterValue string
	}{
		{"vpc-id", "vpc-id", vpcID},
		{"subnet-id", "subnet-id", subnetID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, describeErr := c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
				Filters: []ec2types.Filter{{Name: aws.String(tc.filterName), Values: []string{tc.filterValue}}},
			})
			if describeErr != nil {
				t.Fatalf("DescribeInstances: %v", describeErr)
			}

			ids := instanceIDs(out)
			if len(ids) != 1 || ids[0] != wantID {
				t.Fatalf("filter %s=%s returned %v, want only %q", tc.filterName, tc.filterValue, ids, wantID)
			}
		})
	}
}

// TestDescribeInstancesUnknownFilterErrors pins that an unrecognized filter name
// is rejected with InvalidParameterValue (real EC2), not silently matching every
// instance or nothing.
func TestDescribeInstancesUnknownFilterErrors(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	_ = runOneInstance(t, c)

	_, err := c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{{Name: aws.String("not-a-real-filter"), Values: []string{"x"}}},
	})
	if err == nil {
		t.Fatal("DescribeInstances with an unknown filter succeeded, want an error")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not an API error: %v", err)
	}

	if apiErr.ErrorCode() != "InvalidParameterValue" {
		t.Fatalf("error code = %q, want InvalidParameterValue", apiErr.ErrorCode())
	}
}

// instanceIDs flattens the instance ids across every reservation in a
// DescribeInstances response.
func instanceIDs(out *ec2.DescribeInstancesOutput) []string {
	var ids []string
	for i := range out.Reservations {
		for j := range out.Reservations[i].Instances {
			ids = append(ids, aws.ToString(out.Reservations[i].Instances[j].InstanceId))
		}
	}

	return ids
}
