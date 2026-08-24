package ec2_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"
)

func createSnapshotVolume(t *testing.T, ctx context.Context, client *ec2.Client) string {
	t.Helper()

	cre, err := client.CreateVolume(ctx, &ec2.CreateVolumeInput{
		AvailabilityZone: aws.String("us-east-1a"),
		Size:             aws.Int32(10),
	})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}

	return aws.ToString(cre.VolumeId)
}

// TestCreateSnapshotReportsOwnerAndProgress pins that a snapshot carries an
// OwnerId and a Progress value — empty ownerId breaks owner queries and empty
// progress breaks the SnapshotCompleted waiter.
func TestCreateSnapshotReportsOwnerAndProgress(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)
	volID := createSnapshotVolume(t, ctx, client)

	snap, err := client.CreateSnapshot(ctx, &ec2.CreateSnapshotInput{
		VolumeId:    aws.String(volID),
		Description: aws.String("backup"),
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	if aws.ToString(snap.OwnerId) == "" {
		t.Errorf("CreateSnapshot OwnerId is empty")
	}
	if aws.ToString(snap.Progress) == "" {
		t.Errorf("CreateSnapshot Progress is empty")
	}

	desc, err := client.DescribeSnapshots(ctx, &ec2.DescribeSnapshotsInput{
		SnapshotIds: []string{aws.ToString(snap.SnapshotId)},
	})
	if err != nil {
		t.Fatalf("DescribeSnapshots: %v", err)
	}
	if len(desc.Snapshots) != 1 {
		t.Fatalf("DescribeSnapshots = %d, want 1", len(desc.Snapshots))
	}
	if aws.ToString(desc.Snapshots[0].OwnerId) == "" {
		t.Errorf("DescribeSnapshots OwnerId is empty")
	}
}

// TestDescribeSnapshotsUnknownIDErrors pins that an explicit unknown snapshot
// id returns InvalidSnapshot.NotFound rather than an empty success.
func TestDescribeSnapshotsUnknownIDErrors(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	_, err := client.DescribeSnapshots(ctx, &ec2.DescribeSnapshotsInput{
		SnapshotIds: []string{"snap-000000000000"},
	})
	if err == nil {
		t.Fatalf("DescribeSnapshots(unknown) returned no error")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidSnapshot.NotFound" {
		t.Fatalf("error code = %v, want InvalidSnapshot.NotFound", err)
	}
}

// TestDescribeSnapshotsTagFilter pins snapshot tag filtering.
func TestDescribeSnapshotsTagFilter(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)
	volID := createSnapshotVolume(t, ctx, client)

	for _, name := range []string{"keep", "other"} {
		if _, err := client.CreateSnapshot(ctx, &ec2.CreateSnapshotInput{
			VolumeId: aws.String(volID),
			TagSpecifications: []ec2types.TagSpecification{{
				ResourceType: ec2types.ResourceTypeSnapshot,
				Tags:         []ec2types.Tag{{Key: aws.String("Name"), Value: aws.String(name)}},
			}},
		}); err != nil {
			t.Fatalf("CreateSnapshot(%s): %v", name, err)
		}
	}

	out, err := client.DescribeSnapshots(ctx, &ec2.DescribeSnapshotsInput{
		Filters: []ec2types.Filter{{Name: aws.String("tag:Name"), Values: []string{"keep"}}},
	})
	if err != nil {
		t.Fatalf("DescribeSnapshots: %v", err)
	}
	if len(out.Snapshots) != 1 {
		t.Fatalf("tag:Name=keep returned %d snapshots, want 1", len(out.Snapshots))
	}
}

// TestModifySnapshotAttributeAccepted pins that the previously-undispatched
// ModifySnapshotAttribute action succeeds for an existing snapshot.
func TestModifySnapshotAttributeAccepted(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)
	volID := createSnapshotVolume(t, ctx, client)

	snap, err := client.CreateSnapshot(ctx, &ec2.CreateSnapshotInput{VolumeId: aws.String(volID)})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	if _, err := client.ModifySnapshotAttribute(ctx, &ec2.ModifySnapshotAttributeInput{
		SnapshotId:    snap.SnapshotId,
		Attribute:     ec2types.SnapshotAttributeNameCreateVolumePermission,
		OperationType: ec2types.OperationTypeAdd,
		GroupNames:    []string{"all"},
	}); err != nil {
		t.Fatalf("ModifySnapshotAttribute: %v", err)
	}
}

// TestCopySnapshotClonesSourceAndTags pins that CopySnapshot (previously
// undispatched) returns a fresh snapshot id, copies the source size, applies
// the requested description and tags, and leaves the source untouched.
func TestCopySnapshotClonesSourceAndTags(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)
	volID := createSnapshotVolume(t, ctx, client)

	src, err := client.CreateSnapshot(ctx, &ec2.CreateSnapshotInput{
		VolumeId:    aws.String(volID),
		Description: aws.String("orig"),
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	cp, err := client.CopySnapshot(ctx, &ec2.CopySnapshotInput{
		SourceRegion:     aws.String("us-east-1"),
		SourceSnapshotId: src.SnapshotId,
		Description:      aws.String("copy"),
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeSnapshot,
			Tags:         []ec2types.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
		}},
	})
	if err != nil {
		t.Fatalf("CopySnapshot: %v", err)
	}

	newID := aws.ToString(cp.SnapshotId)
	if newID == "" || newID == aws.ToString(src.SnapshotId) {
		t.Fatalf("CopySnapshot id = %q, want fresh id != source %q", newID, aws.ToString(src.SnapshotId))
	}

	if !hasTag(cp.Tags, "env", "prod") {
		t.Errorf("CopySnapshot response tags = %+v, want env=prod", cp.Tags)
	}

	desc, err := client.DescribeSnapshots(ctx, &ec2.DescribeSnapshotsInput{
		SnapshotIds: []string{newID},
	})
	if err != nil {
		t.Fatalf("DescribeSnapshots(copy): %v", err)
	}
	if len(desc.Snapshots) != 1 {
		t.Fatalf("DescribeSnapshots = %d, want 1", len(desc.Snapshots))
	}

	s := desc.Snapshots[0]
	if aws.ToInt32(s.VolumeSize) != 10 {
		t.Errorf("copy VolumeSize = %d, want 10 (source size)", aws.ToInt32(s.VolumeSize))
	}
	if aws.ToString(s.Description) != "copy" {
		t.Errorf("copy Description = %q, want %q", aws.ToString(s.Description), "copy")
	}
	if !hasTag(s.Tags, "env", "prod") {
		t.Errorf("copy tags = %+v, want env=prod", s.Tags)
	}
}

// TestCopySnapshotMissingSourceIsNotFound pins the AWS error code for a copy of
// a non-existent source snapshot.
func TestCopySnapshotMissingSourceIsNotFound(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	_, err := client.CopySnapshot(ctx, &ec2.CopySnapshotInput{
		SourceRegion:     aws.String("us-east-1"),
		SourceSnapshotId: aws.String("snap-000000000000"),
	})
	if err == nil {
		t.Fatal("CopySnapshot(missing source) succeeded, want error")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidSnapshot.NotFound" {
		t.Fatalf("CopySnapshot error = %v, want InvalidSnapshot.NotFound", err)
	}
}

// hasTag reports whether tags contains key=value.
func hasTag(tags []ec2types.Tag, key, value string) bool {
	for _, tg := range tags {
		if aws.ToString(tg.Key) == key && aws.ToString(tg.Value) == value {
			return true
		}
	}

	return false
}
