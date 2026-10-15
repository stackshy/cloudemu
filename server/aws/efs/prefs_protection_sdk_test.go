package efs_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsefs "github.com/aws/aws-sdk-go-v2/service/efs"
	efstypes "github.com/aws/aws-sdk-go-v2/service/efs/types"
)

// TestSDKAccountPreferencesDefault checks an account that never set a
// preference reports LONG_ID for file systems and mount targets, and that
// SHORT_ID can no longer be set.
func TestSDKAccountPreferencesDefault(t *testing.T) {
	ctx := context.Background()
	c := newEFSClient(t)

	got, err := c.DescribeAccountPreferences(ctx, &awsefs.DescribeAccountPreferencesInput{})
	if err != nil {
		t.Fatalf("DescribeAccountPreferences: %v", err)
	}

	pref := got.ResourceIdPreference
	if pref == nil || pref.ResourceIdType != efstypes.ResourceIdTypeLongId || len(pref.Resources) != 2 ||
		pref.Resources[0] != efstypes.ResourceFileSystem || pref.Resources[1] != efstypes.ResourceMountTarget {
		t.Fatalf("default preference = %+v, want LONG_ID for FILE_SYSTEM and MOUNT_TARGET", pref)
	}

	var bad *efstypes.BadRequest

	_, err = c.PutAccountPreferences(ctx, &awsefs.PutAccountPreferencesInput{ResourceIdType: efstypes.ResourceIdTypeShortId})
	if !errors.As(err, &bad) {
		t.Fatalf("PutAccountPreferences(SHORT_ID): want BadRequest, got %v", err)
	}
}

// TestSDKProtectionReplicationAlreadyExists checks UpdateFileSystemProtection on
// a replication source or destination returns ReplicationAlreadyExists (409).
func TestSDKProtectionReplicationAlreadyExists(t *testing.T) {
	ctx := context.Background()
	c := newEFSClient(t)

	src, _ := c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{CreationToken: aws.String("rep-src")})
	dst, _ := c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{CreationToken: aws.String("rep-dst")})

	if _, err := c.CreateReplicationConfiguration(ctx, &awsefs.CreateReplicationConfigurationInput{
		SourceFileSystemId: src.FileSystemId,
		Destinations:       []efstypes.DestinationToCreate{{FileSystemId: dst.FileSystemId}},
	}); err != nil {
		t.Fatalf("CreateReplicationConfiguration: %v", err)
	}

	for _, id := range []*string{src.FileSystemId, dst.FileSystemId} {
		var exists *efstypes.ReplicationAlreadyExists

		_, err := c.UpdateFileSystemProtection(ctx, &awsefs.UpdateFileSystemProtectionInput{
			FileSystemId: id, ReplicationOverwriteProtection: efstypes.ReplicationOverwriteProtectionEnabled,
		})
		if !errors.As(err, &exists) {
			t.Fatalf("UpdateFileSystemProtection(%s): want ReplicationAlreadyExists, got %v", aws.ToString(id), err)
		}
	}
}
