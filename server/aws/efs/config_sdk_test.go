package efs_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsefs "github.com/aws/aws-sdk-go-v2/service/efs"
	efstypes "github.com/aws/aws-sdk-go-v2/service/efs/types"
)

func TestSDKLifecycleConfiguration(t *testing.T) {
	ctx := context.Background()
	c := newEFSClient(t)

	fs, _ := c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{CreationToken: aws.String("lc-1")})
	fsID := aws.ToString(fs.FileSystemId)

	if _, err := c.PutLifecycleConfiguration(ctx, &awsefs.PutLifecycleConfigurationInput{
		FileSystemId: aws.String(fsID),
		LifecyclePolicies: []efstypes.LifecyclePolicy{
			{TransitionToIA: efstypes.TransitionToIARulesAfter30Days},
			{TransitionToPrimaryStorageClass: efstypes.TransitionToPrimaryStorageClassRulesAfter1Access},
		},
	}); err != nil {
		t.Fatalf("PutLifecycleConfiguration: %v", err)
	}

	got, err := c.DescribeLifecycleConfiguration(ctx, &awsefs.DescribeLifecycleConfigurationInput{
		FileSystemId: aws.String(fsID),
	})
	if err != nil {
		t.Fatalf("DescribeLifecycleConfiguration: %v", err)
	}

	if len(got.LifecyclePolicies) != 2 {
		t.Fatalf("want 2 lifecycle policies, got %d", len(got.LifecyclePolicies))
	}

	if got.LifecyclePolicies[0].TransitionToIA != efstypes.TransitionToIARulesAfter30Days {
		t.Fatalf("unexpected TransitionToIA: %s", got.LifecyclePolicies[0].TransitionToIA)
	}
}

func TestSDKBackupPolicy(t *testing.T) {
	ctx := context.Background()
	c := newEFSClient(t)

	fs, _ := c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{CreationToken: aws.String("bk-1")})
	fsID := aws.ToString(fs.FileSystemId)

	// Default is DISABLED.
	got, err := c.DescribeBackupPolicy(ctx, &awsefs.DescribeBackupPolicyInput{FileSystemId: aws.String(fsID)})
	if err != nil {
		t.Fatalf("DescribeBackupPolicy: %v", err)
	}

	if got.BackupPolicy.Status != efstypes.StatusDisabled {
		t.Fatalf("default backup status = %s, want DISABLED", got.BackupPolicy.Status)
	}

	if _, err := c.PutBackupPolicy(ctx, &awsefs.PutBackupPolicyInput{
		FileSystemId: aws.String(fsID),
		BackupPolicy: &efstypes.BackupPolicy{Status: efstypes.StatusEnabled},
	}); err != nil {
		t.Fatalf("PutBackupPolicy: %v", err)
	}

	got, _ = c.DescribeBackupPolicy(ctx, &awsefs.DescribeBackupPolicyInput{FileSystemId: aws.String(fsID)})
	if got.BackupPolicy.Status != efstypes.StatusEnabled {
		t.Fatalf("backup status = %s, want ENABLED", got.BackupPolicy.Status)
	}
}

func TestSDKReplicationConfiguration(t *testing.T) {
	ctx := context.Background()
	c := newEFSClient(t)

	fs, _ := c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{CreationToken: aws.String("rep-1")})
	fsID := aws.ToString(fs.FileSystemId)

	rc, err := c.CreateReplicationConfiguration(ctx, &awsefs.CreateReplicationConfigurationInput{
		SourceFileSystemId: aws.String(fsID),
		Destinations:       []efstypes.DestinationToCreate{{Region: aws.String("us-west-2")}},
	})
	if err != nil {
		t.Fatalf("CreateReplicationConfiguration: %v", err)
	}

	if aws.ToString(rc.SourceFileSystemId) != fsID || len(rc.Destinations) != 1 {
		t.Fatalf("unexpected replication config: %+v", rc)
	}

	if aws.ToString(rc.Destinations[0].FileSystemId) == "" {
		t.Fatal("destination file system id should be assigned")
	}

	// Describe (source-scoped) + describe-all.
	desc, err := c.DescribeReplicationConfigurations(ctx, &awsefs.DescribeReplicationConfigurationsInput{
		FileSystemId: aws.String(fsID),
	})
	if err != nil || len(desc.Replications) != 1 {
		t.Fatalf("DescribeReplicationConfigurations(fs) = %d, %v", len(desc.Replications), err)
	}

	all, err := c.DescribeReplicationConfigurations(ctx, &awsefs.DescribeReplicationConfigurationsInput{})
	if err != nil || len(all.Replications) != 1 {
		t.Fatalf("DescribeReplicationConfigurations(all) = %d, %v", len(all.Replications), err)
	}

	if _, err := c.DeleteReplicationConfiguration(ctx, &awsefs.DeleteReplicationConfigurationInput{
		SourceFileSystemId: aws.String(fsID),
	}); err != nil {
		t.Fatalf("DeleteReplicationConfiguration: %v", err)
	}
}

func TestSDKAccountPreferences(t *testing.T) {
	ctx := context.Background()
	c := newEFSClient(t)

	if _, err := c.PutAccountPreferences(ctx, &awsefs.PutAccountPreferencesInput{
		ResourceIdType: efstypes.ResourceIdTypeLongId,
	}); err != nil {
		t.Fatalf("PutAccountPreferences: %v", err)
	}

	got, err := c.DescribeAccountPreferences(ctx, &awsefs.DescribeAccountPreferencesInput{})
	if err != nil {
		t.Fatalf("DescribeAccountPreferences: %v", err)
	}

	if got.ResourceIdPreference == nil || got.ResourceIdPreference.ResourceIdType != efstypes.ResourceIdTypeLongId {
		t.Fatalf("unexpected preference: %+v", got.ResourceIdPreference)
	}
}
