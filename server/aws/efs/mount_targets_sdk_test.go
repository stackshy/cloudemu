package efs_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsefs "github.com/aws/aws-sdk-go-v2/service/efs"
	efstypes "github.com/aws/aws-sdk-go-v2/service/efs/types"
)

func TestSDKMountTargets(t *testing.T) {
	ctx := context.Background()
	c := newEFSClient(t)

	fs, _ := c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{CreationToken: aws.String("mt-1")})
	fsID := aws.ToString(fs.FileSystemId)

	mt, err := c.CreateMountTarget(ctx, &awsefs.CreateMountTargetInput{
		FileSystemId:   aws.String(fsID),
		SubnetId:       aws.String("subnet-123"),
		SecurityGroups: []string{"sg-1"},
	})
	if err != nil {
		t.Fatalf("CreateMountTarget: %v", err)
	}

	mtID := aws.ToString(mt.MountTargetId)
	if mtID == "" || mt.LifeCycleState != efstypes.LifeCycleStateAvailable {
		t.Fatalf("unexpected mount target: %+v", mt)
	}

	// A file system with a mount target can't be deleted.
	if _, err := c.DeleteFileSystem(ctx, &awsefs.DeleteFileSystemInput{FileSystemId: aws.String(fsID)}); err == nil {
		t.Fatal("DeleteFileSystem should fail while a mount target exists")
	}

	// Describe by file system reflects the mount target and count.
	mts, err := c.DescribeMountTargets(ctx, &awsefs.DescribeMountTargetsInput{FileSystemId: aws.String(fsID)})
	if err != nil {
		t.Fatalf("DescribeMountTargets: %v", err)
	}

	if len(mts.MountTargets) != 1 {
		t.Fatalf("want 1 mount target, got %d", len(mts.MountTargets))
	}

	desc, _ := c.DescribeFileSystems(ctx, &awsefs.DescribeFileSystemsInput{FileSystemId: aws.String(fsID)})
	if desc.FileSystems[0].NumberOfMountTargets != 1 {
		t.Fatalf("NumberOfMountTargets = %d, want 1", desc.FileSystems[0].NumberOfMountTargets)
	}

	// Security groups get/modify.
	sgs, err := c.DescribeMountTargetSecurityGroups(ctx, &awsefs.DescribeMountTargetSecurityGroupsInput{
		MountTargetId: aws.String(mtID),
	})
	if err != nil || len(sgs.SecurityGroups) != 1 || sgs.SecurityGroups[0] != "sg-1" {
		t.Fatalf("DescribeMountTargetSecurityGroups = %+v, %v", sgs.SecurityGroups, err)
	}

	if _, err := c.ModifyMountTargetSecurityGroups(ctx, &awsefs.ModifyMountTargetSecurityGroupsInput{
		MountTargetId: aws.String(mtID), SecurityGroups: []string{"sg-2", "sg-3"},
	}); err != nil {
		t.Fatalf("ModifyMountTargetSecurityGroups: %v", err)
	}

	sgs, _ = c.DescribeMountTargetSecurityGroups(ctx, &awsefs.DescribeMountTargetSecurityGroupsInput{
		MountTargetId: aws.String(mtID),
	})
	if len(sgs.SecurityGroups) != 2 {
		t.Fatalf("modified SGs = %+v, want 2", sgs.SecurityGroups)
	}

	// Delete the mount target, then the file system deletes fine.
	if _, err := c.DeleteMountTarget(ctx, &awsefs.DeleteMountTargetInput{MountTargetId: aws.String(mtID)}); err != nil {
		t.Fatalf("DeleteMountTarget: %v", err)
	}

	if _, err := c.DeleteFileSystem(ctx, &awsefs.DeleteFileSystemInput{FileSystemId: aws.String(fsID)}); err != nil {
		t.Fatalf("DeleteFileSystem after mount-target removal: %v", err)
	}
}

func TestSDKAccessPoints(t *testing.T) {
	ctx := context.Background()
	c := newEFSClient(t)

	fs, _ := c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{CreationToken: aws.String("ap-1")})
	fsID := aws.ToString(fs.FileSystemId)

	ap, err := c.CreateAccessPoint(ctx, &awsefs.CreateAccessPointInput{
		FileSystemId: aws.String(fsID),
		PosixUser:    &efstypes.PosixUser{Uid: aws.Int64(1000), Gid: aws.Int64(1000)},
		RootDirectory: &efstypes.RootDirectory{
			Path: aws.String("/app"),
			CreationInfo: &efstypes.CreationInfo{
				OwnerUid: aws.Int64(1000), OwnerGid: aws.Int64(1000), Permissions: aws.String("0755"),
			},
		},
		Tags: []efstypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	})
	if err != nil {
		t.Fatalf("CreateAccessPoint: %v", err)
	}

	apID := aws.ToString(ap.AccessPointId)
	if apID == "" || aws.ToString(ap.AccessPointArn) == "" {
		t.Fatalf("unexpected access point: %+v", ap)
	}

	if aws.ToString(ap.RootDirectory.Path) != "/app" || aws.ToInt64(ap.PosixUser.Uid) != 1000 {
		t.Fatalf("access point fields not preserved: %+v", ap)
	}

	// Describe by file system + by id.
	aps, err := c.DescribeAccessPoints(ctx, &awsefs.DescribeAccessPointsInput{FileSystemId: aws.String(fsID)})
	if err != nil || len(aps.AccessPoints) != 1 {
		t.Fatalf("DescribeAccessPoints(fs) = %d, %v", len(aps.AccessPoints), err)
	}

	byID, err := c.DescribeAccessPoints(ctx, &awsefs.DescribeAccessPointsInput{AccessPointId: aws.String(apID)})
	if err != nil || len(byID.AccessPoints) != 1 {
		t.Fatalf("DescribeAccessPoints(id) = %d, %v", len(byID.AccessPoints), err)
	}

	// Access point is taggable via the shared tag API.
	tags, err := c.ListTagsForResource(ctx, &awsefs.ListTagsForResourceInput{ResourceId: aws.String(apID)})
	if err != nil {
		t.Fatalf("ListTagsForResource(ap): %v", err)
	}

	var found bool
	for _, tg := range tags.Tags {
		if aws.ToString(tg.Key) == "env" {
			found = true
		}
	}

	if !found {
		t.Fatalf("access-point tags not returned: %+v", tags.Tags)
	}

	if _, err := c.DeleteAccessPoint(ctx, &awsefs.DeleteAccessPointInput{AccessPointId: aws.String(apID)}); err != nil {
		t.Fatalf("DeleteAccessPoint: %v", err)
	}
}
