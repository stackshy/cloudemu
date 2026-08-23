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
