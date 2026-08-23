package ec2_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newEC2Client wires a full AWS server and returns a real aws-sdk-go-v2 EC2
// client pointed at it (dummy creds, us-east-1).
func newEC2Client(t *testing.T) *ec2.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	cfg.BaseEndpoint = aws.String(ts.URL)

	return ec2.NewFromConfig(cfg)
}

func mkVPC(ctx context.Context, t *testing.T, c *ec2.Client, cidr string) string {
	t.Helper()

	out, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String(cidr)})
	if err != nil {
		t.Fatalf("CreateVpc(%s): %v", cidr, err)
	}

	return aws.ToString(out.Vpc.VpcId)
}

func mkSubnet(ctx context.Context, t *testing.T, c *ec2.Client, vpcID, cidr, az string) *ec2types.Subnet {
	t.Helper()

	in := &ec2.CreateSubnetInput{VpcId: aws.String(vpcID), CidrBlock: aws.String(cidr)}
	if az != "" {
		in.AvailabilityZone = aws.String(az)
	}

	out, err := c.CreateSubnet(ctx, in)
	if err != nil {
		t.Fatalf("CreateSubnet(%s): %v", cidr, err)
	}

	return out.Subnet
}

// TestCreateSubnetReportsArnZoneIDAndUsableIPs pins that a created subnet carries
// a real subnetArn, the zone id matching the requested AZ, and an
// availableIpAddressCount computed from the CIDR (a /25 has 128-5 = 123 usable),
// not a hardcoded 251.
func TestCreateSubnetReportsArnZoneIDAndUsableIPs(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	vpcID := mkVPC(ctx, t, c, "10.0.0.0/16")
	sub := mkSubnet(ctx, t, c, vpcID, "10.0.1.0/25", "us-east-1a")

	if got := aws.ToInt32(sub.AvailableIpAddressCount); got != 123 {
		t.Errorf("AvailableIpAddressCount = %d, want 123 for a /25", got)
	}

	wantArnSuffix := ":subnet/" + aws.ToString(sub.SubnetId)
	arn := aws.ToString(sub.SubnetArn)
	if !strings.HasPrefix(arn, "arn:aws:ec2:us-east-1:123456789012:subnet/") ||
		!strings.HasSuffix(arn, wantArnSuffix) {
		t.Errorf("SubnetArn = %q, want arn:aws:ec2:us-east-1:123456789012%s", arn, wantArnSuffix)
	}

	if got := aws.ToString(sub.AvailabilityZoneId); got != "us-east-1-az1" {
		t.Errorf("AvailabilityZoneId = %q, want us-east-1-az1", got)
	}

	if got := aws.ToString(sub.OwnerId); got != "123456789012" {
		t.Errorf("OwnerId = %q, want 123456789012", got)
	}
}

// TestModifySubnetAttributeMakesSubnetPublic pins that MapPublicIpOnLaunch is
// off by default and that ModifySubnetAttribute flips it — the only way to build
// a public subnet. Without the fix the attribute is undispatched and the flag
// stays false forever.
func TestModifySubnetAttributeMakesSubnetPublic(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	vpcID := mkVPC(ctx, t, c, "10.0.0.0/16")
	sub := mkSubnet(ctx, t, c, vpcID, "10.0.1.0/24", "us-east-1a")
	subnetID := aws.ToString(sub.SubnetId)

	if aws.ToBool(sub.MapPublicIpOnLaunch) {
		t.Fatalf("new subnet should default MapPublicIpOnLaunch off")
	}

	if _, err := c.ModifySubnetAttribute(ctx, &ec2.ModifySubnetAttributeInput{
		SubnetId:            aws.String(subnetID),
		MapPublicIpOnLaunch: &ec2types.AttributeBooleanValue{Value: aws.Bool(true)},
	}); err != nil {
		t.Fatalf("ModifySubnetAttribute: %v", err)
	}

	desc, err := c.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{SubnetIds: []string{subnetID}})
	if err != nil {
		t.Fatalf("DescribeSubnets: %v", err)
	}

	if len(desc.Subnets) != 1 || !aws.ToBool(desc.Subnets[0].MapPublicIpOnLaunch) {
		t.Fatalf("MapPublicIpOnLaunch not persisted: %+v", desc.Subnets)
	}
}

// TestDescribeSubnetsFilters pins that the vpc-id and cidr filters narrow the
// result to the matching subnet instead of returning subnets from every VPC.
func TestDescribeSubnetsFilters(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	vpcA := mkVPC(ctx, t, c, "10.0.0.0/16")
	vpcB := mkVPC(ctx, t, c, "10.1.0.0/16")
	subA := aws.ToString(mkSubnet(ctx, t, c, vpcA, "10.0.1.0/24", "us-east-1a").SubnetId)
	mkSubnet(ctx, t, c, vpcB, "10.1.1.0/24", "us-east-1a")

	byVPC, err := c.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		Filters: []ec2types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpcA}}},
	})
	if err != nil {
		t.Fatalf("DescribeSubnets(vpc-id): %v", err)
	}

	if len(byVPC.Subnets) != 1 || aws.ToString(byVPC.Subnets[0].SubnetId) != subA {
		t.Fatalf("vpc-id filter returned %d subnets, want only %s", len(byVPC.Subnets), subA)
	}

	byCIDR, err := c.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		Filters: []ec2types.Filter{{Name: aws.String("cidr-block"), Values: []string{"10.1.1.0/24"}}},
	})
	if err != nil {
		t.Fatalf("DescribeSubnets(cidr-block): %v", err)
	}

	if len(byCIDR.Subnets) != 1 || aws.ToString(byCIDR.Subnets[0].VpcId) != vpcB {
		t.Fatalf("cidr-block filter returned %d subnets, want only vpcB's", len(byCIDR.Subnets))
	}
}

// TestDeleteSubnetBlockedByResidentENI pins that an available (unattached) ENI
// in a subnet blocks its deletion with DependencyViolation, matching real EC2.
func TestDeleteSubnetBlockedByResidentENI(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	vpcID := mkVPC(ctx, t, c, "10.0.0.0/16")
	subnetID := aws.ToString(mkSubnet(ctx, t, c, vpcID, "10.0.1.0/24", "us-east-1a").SubnetId)

	eni, err := c.CreateNetworkInterface(ctx, &ec2.CreateNetworkInterfaceInput{
		SubnetId: aws.String(subnetID),
	})
	if err != nil {
		t.Fatalf("CreateNetworkInterface: %v", err)
	}

	if _, err := c.DeleteSubnet(ctx, &ec2.DeleteSubnetInput{SubnetId: aws.String(subnetID)}); err == nil {
		t.Fatalf("DeleteSubnet should fail while an ENI resides in the subnet")
	} else if !strings.Contains(err.Error(), "DependencyViolation") {
		t.Errorf("DeleteSubnet error = %v, want DependencyViolation", err)
	}

	// Draining the ENI lets the delete through — the refusal must be recoverable.
	if _, err := c.DeleteNetworkInterface(ctx, &ec2.DeleteNetworkInterfaceInput{
		NetworkInterfaceId: eni.NetworkInterface.NetworkInterfaceId,
	}); err != nil {
		t.Fatalf("DeleteNetworkInterface: %v", err)
	}

	if _, err := c.DeleteSubnet(ctx, &ec2.DeleteSubnetInput{SubnetId: aws.String(subnetID)}); err != nil {
		t.Errorf("DeleteSubnet after drain: %v", err)
	}
}

// TestCreateSubnetRejectsOverlappingCIDR pins that a second subnet whose CIDR
// overlaps an existing one in the same VPC is rejected with InvalidSubnet.Conflict.
func TestCreateSubnetRejectsOverlappingCIDR(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	vpcID := mkVPC(ctx, t, c, "10.0.0.0/16")
	mkSubnet(ctx, t, c, vpcID, "10.0.1.0/24", "us-east-1a")

	_, err := c.CreateSubnet(ctx, &ec2.CreateSubnetInput{
		VpcId:     aws.String(vpcID),
		CidrBlock: aws.String("10.0.1.0/25"),
	})
	if err == nil {
		t.Fatalf("overlapping subnet CIDR should be rejected")
	}

	if !strings.Contains(err.Error(), "InvalidSubnet.Conflict") {
		t.Errorf("error = %v, want InvalidSubnet.Conflict", err)
	}
}
