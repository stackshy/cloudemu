package efs_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsefs "github.com/aws/aws-sdk-go-v2/service/efs"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// TestSDKMountTargetPlacementFromSubnet verifies that mount targets of one file
// system, placed in different subnets of the same VPC, all report that VPC's id
// and each reflects its subnet's Availability Zone (real EFS behavior).
func TestSDKMountTargetPlacementFromSubnet(t *testing.T) {
	ctx := context.Background()

	cloud := cloudemu.NewAWS()

	vpcInfo, err := cloud.VPC.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	if err != nil {
		t.Fatalf("CreateVPC: %v", err)
	}

	subnetA, err := cloud.VPC.CreateSubnet(ctx, netdriver.SubnetConfig{
		VPCID: vpcInfo.ID, CIDRBlock: "10.0.1.0/24", AvailabilityZone: "us-east-1a",
	})
	if err != nil {
		t.Fatalf("CreateSubnet A: %v", err)
	}

	subnetB, err := cloud.VPC.CreateSubnet(ctx, netdriver.SubnetConfig{
		VPCID: vpcInfo.ID, CIDRBlock: "10.0.2.0/24", AvailabilityZone: "us-east-1b",
	})
	if err != nil {
		t.Fatalf("CreateSubnet B: %v", err)
	}

	srv := awsserver.New(awsserver.Drivers{EFS: cloud.EFS})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	c := awsefs.NewFromConfig(cfg, func(o *awsefs.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	fs, err := c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{CreationToken: aws.String("mt-vpc")})
	if err != nil {
		t.Fatalf("CreateFileSystem: %v", err)
	}

	mtA, err := c.CreateMountTarget(ctx, &awsefs.CreateMountTargetInput{
		FileSystemId: fs.FileSystemId, SubnetId: aws.String(subnetA.ID),
	})
	if err != nil {
		t.Fatalf("CreateMountTarget A: %v", err)
	}

	mtB, err := c.CreateMountTarget(ctx, &awsefs.CreateMountTargetInput{
		FileSystemId: fs.FileSystemId, SubnetId: aws.String(subnetB.ID),
	})
	if err != nil {
		t.Fatalf("CreateMountTarget B: %v", err)
	}

	// Both mount targets live in the same VPC.
	if aws.ToString(mtA.VpcId) != vpcInfo.ID || aws.ToString(mtB.VpcId) != vpcInfo.ID {
		t.Fatalf("VpcId mismatch: A=%q B=%q, want both %q",
			aws.ToString(mtA.VpcId), aws.ToString(mtB.VpcId), vpcInfo.ID)
	}

	// Each mount target reflects its subnet's Availability Zone.
	if aws.ToString(mtA.AvailabilityZoneName) != "us-east-1a" || aws.ToString(mtA.AvailabilityZoneId) != "use1-az1" {
		t.Fatalf("mtA AZ = %q/%q, want us-east-1a/use1-az1",
			aws.ToString(mtA.AvailabilityZoneName), aws.ToString(mtA.AvailabilityZoneId))
	}

	if aws.ToString(mtB.AvailabilityZoneName) != "us-east-1b" || aws.ToString(mtB.AvailabilityZoneId) != "use1-az2" {
		t.Fatalf("mtB AZ = %q/%q, want us-east-1b/use1-az2",
			aws.ToString(mtB.AvailabilityZoneName), aws.ToString(mtB.AvailabilityZoneId))
	}
}
