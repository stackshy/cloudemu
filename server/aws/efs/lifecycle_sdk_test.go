package efs_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsefs "github.com/aws/aws-sdk-go-v2/service/efs"
	efstypes "github.com/aws/aws-sdk-go-v2/service/efs/types"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newAsyncEFSClient serves EFS from a provider with AsyncSettle on a FakeClock,
// so the creating/deleting lifecycle is observable and advanceable.
func newAsyncEFSClient(t *testing.T) (*awsefs.Client, *config.FakeClock) {
	t.Helper()

	clk := config.NewFakeClock(time.Unix(1_700_000_000, 0))
	cloud := cloudemu.NewAWS(config.WithClock(clk), config.WithAsyncSettle())

	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{EFS: cloud.EFS}))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsefs.NewFromConfig(cfg, func(o *awsefs.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	}), clk
}

func TestSDKUpdateFileSystemProtection(t *testing.T) {
	ctx := context.Background()
	c := newEFSClient(t)

	fs, err := c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{CreationToken: aws.String("prot-1")})
	if err != nil {
		t.Fatalf("CreateFileSystem: %v", err)
	}

	out, err := c.UpdateFileSystemProtection(ctx, &awsefs.UpdateFileSystemProtectionInput{
		FileSystemId:                   fs.FileSystemId,
		ReplicationOverwriteProtection: efstypes.ReplicationOverwriteProtectionDisabled,
	})
	if err != nil {
		t.Fatalf("UpdateFileSystemProtection: %v", err)
	}

	if out.ReplicationOverwriteProtection != efstypes.ReplicationOverwriteProtectionDisabled {
		t.Fatalf("response protection = %s, want DISABLED", out.ReplicationOverwriteProtection)
	}

	desc, err := c.DescribeFileSystems(ctx, &awsefs.DescribeFileSystemsInput{FileSystemId: fs.FileSystemId})
	if err != nil {
		t.Fatalf("DescribeFileSystems: %v", err)
	}

	if p := desc.FileSystems[0].FileSystemProtection; p == nil ||
		p.ReplicationOverwriteProtection != efstypes.ReplicationOverwriteProtectionDisabled {
		t.Fatalf("described protection = %+v, want DISABLED", p)
	}

	var nf *efstypes.FileSystemNotFound

	_, err = c.UpdateFileSystemProtection(ctx, &awsefs.UpdateFileSystemProtectionInput{
		FileSystemId:                   aws.String("fs-0123456789abcdef0"),
		ReplicationOverwriteProtection: efstypes.ReplicationOverwriteProtectionEnabled,
	})
	if !errors.As(err, &nf) {
		t.Fatalf("unknown fs: want FileSystemNotFound, got %v", err)
	}

	var bad *efstypes.BadRequest

	_, err = c.UpdateFileSystemProtection(ctx, &awsefs.UpdateFileSystemProtectionInput{
		FileSystemId:                   fs.FileSystemId,
		ReplicationOverwriteProtection: "BOGUS",
	})
	if !errors.As(err, &bad) {
		t.Fatalf("invalid value: want BadRequest, got %v", err)
	}
}

// TestSDKAsyncLifecycle drives the creating -> available -> deleting -> gone
// lifecycle over the wire, including IncorrectFileSystemLifeCycleState.
func TestSDKAsyncLifecycle(t *testing.T) {
	ctx := context.Background()
	c, clk := newAsyncEFSClient(t)

	fs, err := c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{CreationToken: aws.String("async-1")})
	if err != nil {
		t.Fatalf("CreateFileSystem: %v", err)
	}

	if fs.LifeCycleState != efstypes.LifeCycleStateCreating {
		t.Fatalf("create state = %s, want creating", fs.LifeCycleState)
	}

	var incorrect *efstypes.IncorrectFileSystemLifeCycleState

	_, err = c.CreateMountTarget(ctx, &awsefs.CreateMountTargetInput{
		FileSystemId: fs.FileSystemId, SubnetId: aws.String("subnet-0123456789abcdef0"),
	})
	if !errors.As(err, &incorrect) {
		t.Fatalf("CreateMountTarget on creating fs: want IncorrectFileSystemLifeCycleState, got %v", err)
	}

	clk.Advance(time.Minute)

	mt, err := c.CreateMountTarget(ctx, &awsefs.CreateMountTargetInput{
		FileSystemId: fs.FileSystemId, SubnetId: aws.String("subnet-0123456789abcdef0"),
	})
	if err != nil {
		t.Fatalf("CreateMountTarget: %v", err)
	}

	if mt.LifeCycleState != efstypes.LifeCycleStateCreating {
		t.Fatalf("mount target create state = %s, want creating", mt.LifeCycleState)
	}

	clk.Advance(time.Minute)

	if _, err = c.DeleteMountTarget(ctx, &awsefs.DeleteMountTargetInput{MountTargetId: mt.MountTargetId}); err != nil {
		t.Fatalf("DeleteMountTarget: %v", err)
	}

	mts, err := c.DescribeMountTargets(ctx, &awsefs.DescribeMountTargetsInput{MountTargetId: mt.MountTargetId})
	if err != nil || mts.MountTargets[0].LifeCycleState != efstypes.LifeCycleStateDeleting {
		t.Fatalf("mount target after delete = %+v, %v; want deleting", mts, err)
	}

	clk.Advance(time.Minute)

	if _, err = c.DeleteFileSystem(ctx, &awsefs.DeleteFileSystemInput{FileSystemId: fs.FileSystemId}); err != nil {
		t.Fatalf("DeleteFileSystem: %v", err)
	}

	desc, err := c.DescribeFileSystems(ctx, &awsefs.DescribeFileSystemsInput{FileSystemId: fs.FileSystemId})
	if err != nil || desc.FileSystems[0].LifeCycleState != efstypes.LifeCycleStateDeleting {
		t.Fatalf("fs after delete = %+v, %v; want deleting", desc, err)
	}

	clk.Advance(time.Minute)

	var nf *efstypes.FileSystemNotFound

	_, err = c.DescribeFileSystems(ctx, &awsefs.DescribeFileSystemsInput{FileSystemId: fs.FileSystemId})
	if !errors.As(err, &nf) {
		t.Fatalf("fs after delete settle: want FileSystemNotFound, got %v", err)
	}
}
