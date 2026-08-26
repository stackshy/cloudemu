package efs_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
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

// TestSDKEncryptedFileSystemHasDefaultKMSKey verifies an encrypted file system
// created without an explicit KMS key reports the account's default CMK ARN,
// matching real EFS (rather than an empty KmsKeyId).
func TestSDKEncryptedFileSystemHasDefaultKMSKey(t *testing.T) {
	ctx := context.Background()
	c := newEFSClient(t)

	fs, err := c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{
		CreationToken: aws.String("enc-default-key"),
		Encrypted:     aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("CreateFileSystem: %v", err)
	}

	if !aws.ToBool(fs.Encrypted) {
		t.Fatalf("Encrypted = false, want true")
	}

	key := aws.ToString(fs.KmsKeyId)
	if key == "" {
		t.Fatal("encrypted file system has empty KmsKeyId, want a default CMK ARN")
	}

	if !strings.HasPrefix(key, "arn:aws:kms:") || !strings.Contains(key, ":key/") {
		t.Fatalf("KmsKeyId = %q, want a kms key ARN", key)
	}

	// The default must survive a Describe round-trip.
	desc, err := c.DescribeFileSystems(ctx, &awsefs.DescribeFileSystemsInput{FileSystemId: fs.FileSystemId})
	if err != nil {
		t.Fatalf("DescribeFileSystems: %v", err)
	}

	if aws.ToString(desc.FileSystems[0].KmsKeyId) != key {
		t.Fatalf("described KmsKeyId = %q, want %q", aws.ToString(desc.FileSystems[0].KmsKeyId), key)
	}
}

// TestSDKUnencryptedFileSystemHasNoKMSKey verifies an unencrypted file system
// does not get a default CMK.
func TestSDKUnencryptedFileSystemHasNoKMSKey(t *testing.T) {
	ctx := context.Background()
	c := newEFSClient(t)

	fs, err := c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{
		CreationToken: aws.String("unencrypted"),
	})
	if err != nil {
		t.Fatalf("CreateFileSystem: %v", err)
	}

	if aws.ToString(fs.KmsKeyId) != "" {
		t.Fatalf("unencrypted file system KmsKeyId = %q, want empty", aws.ToString(fs.KmsKeyId))
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

func TestSDKDescribeAllFileSystems(t *testing.T) {
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

// TestSDKDeleteWithMountTargetsGuarded verifies DeleteFileSystem is rejected
// while a mount target still exists, and succeeds once it is removed.
func TestSDKDeleteWithMountTargetsGuarded(t *testing.T) {
	ctx := context.Background()
	c := newEFSClient(t)

	fs, err := c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{CreationToken: aws.String("guarded")})
	if err != nil {
		t.Fatalf("CreateFileSystem: %v", err)
	}

	fsID := aws.ToString(fs.FileSystemId)

	mt, err := c.CreateMountTarget(ctx, &awsefs.CreateMountTargetInput{
		FileSystemId: aws.String(fsID),
		SubnetId:     aws.String("subnet-0abcd1234ef567890"),
	})
	if err != nil {
		t.Fatalf("CreateMountTarget: %v", err)
	}

	if _, err := c.DeleteFileSystem(ctx, &awsefs.DeleteFileSystemInput{FileSystemId: aws.String(fsID)}); err == nil {
		t.Fatal("DeleteFileSystem with mount target: want error, got nil")
	}

	if _, err := c.DeleteMountTarget(ctx, &awsefs.DeleteMountTargetInput{
		MountTargetId: mt.MountTargetId,
	}); err != nil {
		t.Fatalf("DeleteMountTarget: %v", err)
	}

	if _, err := c.DeleteFileSystem(ctx, &awsefs.DeleteFileSystemInput{FileSystemId: aws.String(fsID)}); err != nil {
		t.Fatalf("DeleteFileSystem after MT removed: %v", err)
	}
}

// TestSDKMountTargetPerSubnetConflict verifies a second mount target in the
// same subnet is rejected with a MountTargetConflict-typed error.
func TestSDKMountTargetPerSubnetConflict(t *testing.T) {
	ctx := context.Background()
	c := newEFSClient(t)

	fs, err := c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{CreationToken: aws.String("subnet-conflict")})
	if err != nil {
		t.Fatalf("CreateFileSystem: %v", err)
	}

	in := &awsefs.CreateMountTargetInput{
		FileSystemId: fs.FileSystemId,
		SubnetId:     aws.String("subnet-0abcd1234ef567890"),
	}

	if _, err := c.CreateMountTarget(ctx, in); err != nil {
		t.Fatalf("CreateMountTarget: %v", err)
	}

	_, err = c.CreateMountTarget(ctx, in)
	if err == nil {
		t.Fatal("second mount target in same subnet: want error, got nil")
	}

	var conflict *efstypes.MountTargetConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("want MountTargetConflict, got %T: %v", err, err)
	}
}

// TestSDKTypedNotFoundErrors verifies each resource surfaces its own typed
// not-found exception rather than collapsing to FileSystemNotFound.
func TestSDKTypedNotFoundErrors(t *testing.T) {
	ctx := context.Background()
	c := newEFSClient(t)

	if _, err := c.DescribeMountTargetSecurityGroups(ctx, &awsefs.DescribeMountTargetSecurityGroupsInput{
		MountTargetId: aws.String("fsmt-does-not-exist"),
	}); err == nil {
		t.Fatal("want MountTargetNotFound, got nil")
	} else {
		var nf *efstypes.MountTargetNotFound
		if !errors.As(err, &nf) {
			t.Fatalf("want MountTargetNotFound, got %T: %v", err, err)
		}
	}

	if _, err := c.DescribeAccessPoints(ctx, &awsefs.DescribeAccessPointsInput{
		AccessPointId: aws.String("fsap-does-not-exist"),
	}); err == nil {
		t.Fatal("want AccessPointNotFound, got nil")
	} else {
		var nf *efstypes.AccessPointNotFound
		if !errors.As(err, &nf) {
			t.Fatalf("want AccessPointNotFound, got %T: %v", err, err)
		}
	}

	if _, err := c.DescribeFileSystems(ctx, &awsefs.DescribeFileSystemsInput{
		FileSystemId: aws.String("fs-does-not-exist"),
	}); err == nil {
		t.Fatal("want FileSystemNotFound, got nil")
	} else {
		var nf *efstypes.FileSystemNotFound
		if !errors.As(err, &nf) {
			t.Fatalf("want FileSystemNotFound, got %T: %v", err, err)
		}
	}
}

// TestSDKDescribeMountTargetsByID exercises describing mount targets by
// mount-target id and by access-point id.
func TestSDKDescribeMountTargetsByID(t *testing.T) {
	ctx := context.Background()
	c := newEFSClient(t)

	fs, err := c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{CreationToken: aws.String("describe-mt")})
	if err != nil {
		t.Fatalf("CreateFileSystem: %v", err)
	}

	fsID := aws.ToString(fs.FileSystemId)

	mt, err := c.CreateMountTarget(ctx, &awsefs.CreateMountTargetInput{
		FileSystemId: aws.String(fsID),
		SubnetId:     aws.String("subnet-0abcd1234ef567890"),
	})
	if err != nil {
		t.Fatalf("CreateMountTarget: %v", err)
	}

	ap, err := c.CreateAccessPoint(ctx, &awsefs.CreateAccessPointInput{
		FileSystemId: aws.String(fsID),
	})
	if err != nil {
		t.Fatalf("CreateAccessPoint: %v", err)
	}

	byMT, err := c.DescribeMountTargets(ctx, &awsefs.DescribeMountTargetsInput{
		MountTargetId: mt.MountTargetId,
	})
	if err != nil {
		t.Fatalf("DescribeMountTargets(byMT): %v", err)
	}

	if len(byMT.MountTargets) != 1 || aws.ToString(byMT.MountTargets[0].MountTargetId) != aws.ToString(mt.MountTargetId) {
		t.Fatalf("describe by MT id: unexpected result %+v", byMT.MountTargets)
	}

	byAP, err := c.DescribeMountTargets(ctx, &awsefs.DescribeMountTargetsInput{
		AccessPointId: ap.AccessPointId,
	})
	if err != nil {
		t.Fatalf("DescribeMountTargets(byAP): %v", err)
	}

	if len(byAP.MountTargets) != 1 {
		t.Fatalf("describe by AP id: want 1 mount target, got %d", len(byAP.MountTargets))
	}
}

// TestSDKDuplicateTokenCarriesFileSystemId verifies a duplicate CreationToken is
// rejected with a FileSystemAlreadyExists error that carries the existing file
// system's id, so an idempotent CreateFileSystem retry can recover it.
func TestSDKDuplicateTokenCarriesFileSystemId(t *testing.T) {
	ctx := context.Background()
	c := newEFSClient(t)

	first, err := c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{CreationToken: aws.String("idem-token")})
	if err != nil {
		t.Fatalf("first CreateFileSystem: %v", err)
	}

	_, err = c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{CreationToken: aws.String("idem-token")})
	if err == nil {
		t.Fatal("duplicate token: want FileSystemAlreadyExists, got nil")
	}

	var already *efstypes.FileSystemAlreadyExists
	if !errors.As(err, &already) {
		t.Fatalf("want FileSystemAlreadyExists, got %T: %v", err, err)
	}

	if aws.ToString(already.FileSystemId) != aws.ToString(first.FileSystemId) {
		t.Fatalf("FileSystemAlreadyExists.FileSystemId = %q, want %q",
			aws.ToString(already.FileSystemId), aws.ToString(first.FileSystemId))
	}
}
