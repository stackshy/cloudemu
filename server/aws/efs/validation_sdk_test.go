package efs_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsefs "github.com/aws/aws-sdk-go-v2/service/efs"
	"github.com/aws/aws-sdk-go-v2/service/efs/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func requireEFSCode(t *testing.T, err error, want string) {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != want {
		t.Fatalf("err = %v, want code %s", err, want)
	}
}

func TestSDKFileSystemValidation(t *testing.T) {
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
		t.Fatalf("CreateSubnet: %v", err)
	}

	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{EFS: cloud.EFS}))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	c := awsefs.NewFromConfig(cfg, func(o *awsefs.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	_, err = c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{
		CreationToken: aws.String("t1"), PerformanceMode: types.PerformanceMode("bogusMode"),
	})
	requireEFSCode(t, err, "BadRequest")

	_, err = c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{
		CreationToken: aws.String("t2"), ThroughputMode: types.ThroughputModeProvisioned,
		ProvisionedThroughputInMibps: aws.Float64(999999),
	})
	requireEFSCode(t, err, "BadRequest")

	fs, err := c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{
		CreationToken: aws.String("t3"), AvailabilityZoneName: aws.String("us-east-1b"),
	})
	if err != nil {
		t.Fatalf("CreateFileSystem: %v", err)
	}

	_, err = c.UpdateFileSystem(ctx, &awsefs.UpdateFileSystemInput{
		FileSystemId: fs.FileSystemId, ThroughputMode: types.ThroughputModeProvisioned,
	})
	requireEFSCode(t, err, "BadRequest")

	_, err = c.CreateMountTarget(ctx, &awsefs.CreateMountTargetInput{
		FileSystemId: fs.FileSystemId, SubnetId: aws.String(subnetA.ID),
	})
	requireEFSCode(t, err, "AvailabilityZonesMismatch")
}
