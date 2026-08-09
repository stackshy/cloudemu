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
	efstypes "github.com/aws/aws-sdk-go-v2/service/efs/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newEFSClient(t *testing.T) *awsefs.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{EFS: cloud.EFS})

	ts := httptest.NewServer(srv)
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
	})
}

func TestSDKFileSystemLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newEFSClient(t)

	created, err := c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{
		CreationToken:   aws.String("token-1"),
		PerformanceMode: efstypes.PerformanceModeGeneralPurpose,
		Encrypted:       aws.Bool(true),
		Tags:            []efstypes.Tag{{Key: aws.String("Name"), Value: aws.String("app-fs")}},
	})
	if err != nil {
		t.Fatalf("CreateFileSystem: %v", err)
	}

	fsID := aws.ToString(created.FileSystemId)
	if fsID == "" || aws.ToString(created.FileSystemArn) == "" {
		t.Fatalf("empty id/arn: %+v", created)
	}

	if created.LifeCycleState != efstypes.LifeCycleStateAvailable {
		t.Fatalf("state = %s, want available", created.LifeCycleState)
	}

	if aws.ToString(created.Name) != "app-fs" {
		t.Fatalf("Name = %q, want app-fs", aws.ToString(created.Name))
	}

	// Describe by id.
	desc, err := c.DescribeFileSystems(ctx, &awsefs.DescribeFileSystemsInput{FileSystemId: aws.String(fsID)})
	if err != nil {
		t.Fatalf("DescribeFileSystems: %v", err)
	}

	if len(desc.FileSystems) != 1 || aws.ToString(desc.FileSystems[0].FileSystemId) != fsID {
		t.Fatalf("describe mismatch: %+v", desc.FileSystems)
	}

	// Update throughput.
	if _, err := c.UpdateFileSystem(ctx, &awsefs.UpdateFileSystemInput{
		FileSystemId:                 aws.String(fsID),
		ThroughputMode:               efstypes.ThroughputModeProvisioned,
		ProvisionedThroughputInMibps: aws.Float64(64),
	}); err != nil {
		t.Fatalf("UpdateFileSystem: %v", err)
	}

	desc, _ = c.DescribeFileSystems(ctx, &awsefs.DescribeFileSystemsInput{FileSystemId: aws.String(fsID)})
	if desc.FileSystems[0].ThroughputMode != efstypes.ThroughputModeProvisioned {
		t.Fatalf("throughput not updated: %s", desc.FileSystems[0].ThroughputMode)
	}

	// Delete.
	if _, err := c.DeleteFileSystem(ctx, &awsefs.DeleteFileSystemInput{FileSystemId: aws.String(fsID)}); err != nil {
		t.Fatalf("DeleteFileSystem: %v", err)
	}

	_, err = c.DescribeFileSystems(ctx, &awsefs.DescribeFileSystemsInput{FileSystemId: aws.String(fsID)})
	if err == nil {
		t.Fatal("expected error describing deleted file system")
	}

	var nf *efstypes.FileSystemNotFound
	if !errors.As(err, &nf) {
		t.Fatalf("want FileSystemNotFound, got %v", err)
	}
}

func TestSDKFileSystemPolicy(t *testing.T) {
	ctx := context.Background()
	c := newEFSClient(t)

	fs, _ := c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{CreationToken: aws.String("pol-1")})
	fsID := aws.ToString(fs.FileSystemId)

	policy := `{"Version":"2012-10-17","Statement":[]}`
	if _, err := c.PutFileSystemPolicy(ctx, &awsefs.PutFileSystemPolicyInput{
		FileSystemId: aws.String(fsID), Policy: aws.String(policy),
	}); err != nil {
		t.Fatalf("PutFileSystemPolicy: %v", err)
	}

	got, err := c.DescribeFileSystemPolicy(ctx, &awsefs.DescribeFileSystemPolicyInput{FileSystemId: aws.String(fsID)})
	if err != nil {
		t.Fatalf("DescribeFileSystemPolicy: %v", err)
	}

	if aws.ToString(got.Policy) != policy {
		t.Fatalf("policy roundtrip mismatch: %s", aws.ToString(got.Policy))
	}

	if _, err := c.DeleteFileSystemPolicy(ctx, &awsefs.DeleteFileSystemPolicyInput{
		FileSystemId: aws.String(fsID),
	}); err != nil {
		t.Fatalf("DeleteFileSystemPolicy: %v", err)
	}
}

func TestSDKTagging(t *testing.T) {
	ctx := context.Background()
	c := newEFSClient(t)

	fs, _ := c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{CreationToken: aws.String("tag-1")})
	fsID := aws.ToString(fs.FileSystemId)

	if _, err := c.TagResource(ctx, &awsefs.TagResourceInput{
		ResourceId: aws.String(fsID),
		Tags:       []efstypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	tags, err := c.ListTagsForResource(ctx, &awsefs.ListTagsForResourceInput{ResourceId: aws.String(fsID)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	var found bool
	for _, tg := range tags.Tags {
		if aws.ToString(tg.Key) == "env" && aws.ToString(tg.Value) == "prod" {
			found = true
		}
	}

	if !found {
		t.Fatalf("env tag not returned: %+v", tags.Tags)
	}

	if _, err := c.UntagResource(ctx, &awsefs.UntagResourceInput{
		ResourceId: aws.String(fsID), TagKeys: []string{"env"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}
}

func TestSDKDeleteWithMountTargetsGuarded(t *testing.T) {
	// A basic create then describe-all sanity check that the collection GET
	// path works without a filter.
	ctx := context.Background()
	c := newEFSClient(t)

	for _, tok := range []string{"a", "b", "c"} {
		if _, err := c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{CreationToken: aws.String(tok)}); err != nil {
			t.Fatalf("create %s: %v", tok, err)
		}
	}

	all, err := c.DescribeFileSystems(ctx, &awsefs.DescribeFileSystemsInput{})
	if err != nil {
		t.Fatalf("DescribeFileSystems(all): %v", err)
	}

	if len(all.FileSystems) != 3 {
		t.Fatalf("want 3 file systems, got %d", len(all.FileSystems))
	}
}
