package ec2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// TestCreateVpcInstanceTenancyRoundTrips pins that a VPC created with
// InstanceTenancy=dedicated reads back as "dedicated" on DescribeVpcs rather
// than the hardcoded "default" — the perpetual-drift bug on
// aws_vpc.instance_tenancy.
func TestCreateVpcInstanceTenancyRoundTrips(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	created, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{
		CidrBlock:       aws.String("10.0.0.0/16"),
		InstanceTenancy: ec2types.TenancyDedicated,
	})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	if created.Vpc.InstanceTenancy != ec2types.TenancyDedicated {
		t.Errorf("CreateVpc InstanceTenancy = %q, want dedicated", created.Vpc.InstanceTenancy)
	}

	vpcID := aws.ToString(created.Vpc.VpcId)

	out, err := c.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{VpcIds: []string{vpcID}})
	if err != nil {
		t.Fatalf("DescribeVpcs: %v", err)
	}

	if got := out.Vpcs[0].InstanceTenancy; got != ec2types.TenancyDedicated {
		t.Errorf("DescribeVpcs InstanceTenancy = %q, want dedicated", got)
	}
}

// TestCreateVpcInstanceTenancyDefaultsWhenUnset pins that omitting InstanceTenancy
// yields "default".
func TestCreateVpcInstanceTenancyDefaultsWhenUnset(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	vpcID := mkVPC(ctx, t, c, "10.0.0.0/16")

	out, err := c.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{VpcIds: []string{vpcID}})
	if err != nil {
		t.Fatalf("DescribeVpcs: %v", err)
	}

	if got := out.Vpcs[0].InstanceTenancy; got != ec2types.TenancyDefault {
		t.Errorf("DescribeVpcs InstanceTenancy = %q, want default", got)
	}
}

// TestCreateVpcInstanceTenancyInvalidRejected pins that a bogus tenancy — and the
// "host" value, which real EC2 CreateVpc rejects — both fail with
// InvalidParameterValue rather than being silently accepted.
func TestCreateVpcInstanceTenancyInvalidRejected(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	for _, tenancy := range []ec2types.Tenancy{ec2types.TenancyHost, "bogus"} {
		_, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{
			CidrBlock:       aws.String("10.0.0.0/16"),
			InstanceTenancy: tenancy,
		})
		if err == nil {
			t.Fatalf("CreateVpc(InstanceTenancy=%q): expected error, got nil", tenancy)
		}

		if code := apiCode(t, err); code != "InvalidParameterValue" {
			t.Errorf("CreateVpc(InstanceTenancy=%q) error code = %q, want InvalidParameterValue", tenancy, code)
		}
	}
}
