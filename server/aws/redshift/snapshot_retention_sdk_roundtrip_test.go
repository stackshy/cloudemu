package redshift_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsredshift "github.com/aws/aws-sdk-go-v2/service/redshift"
	smithy "github.com/aws/smithy-go"
)

func createRetentionCluster(t *testing.T, client *awsredshift.Client) {
	t.Helper()

	if _, err := client.CreateCluster(context.Background(), &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String("ret"),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("Sup3rSecret!"),
		NodeType:           aws.String("ra3.xlplus"),
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
}

func describeRetention(t *testing.T, client *awsredshift.Client, id string) int32 {
	t.Helper()

	out, err := client.DescribeClusterSnapshots(context.Background(), &awsredshift.DescribeClusterSnapshotsInput{
		SnapshotIdentifier: aws.String(id),
	})
	if err != nil {
		t.Fatalf("DescribeClusterSnapshots(%s): %v", id, err)
	}

	return aws.ToInt32(out.Snapshots[0].ManualSnapshotRetentionPeriod)
}

func requireAPIErrorCode(t *testing.T, err error, code string) {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

func TestSDKRedshiftSnapshotRetentionDefaultAndGiven(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()
	createRetentionCluster(t, client)

	created, err := client.CreateClusterSnapshot(ctx, &awsredshift.CreateClusterSnapshotInput{
		ClusterIdentifier: aws.String("ret"), SnapshotIdentifier: aws.String("s-default"),
	})
	if err != nil {
		t.Fatalf("CreateClusterSnapshot: %v", err)
	}

	if got := aws.ToInt32(created.Snapshot.ManualSnapshotRetentionPeriod); got != -1 {
		t.Fatalf("create response retention = %d, want -1", got)
	}

	if got := describeRetention(t, client, "s-default"); got != -1 {
		t.Fatalf("omitted retention reads back %d, want -1", got)
	}

	if _, err := client.CreateClusterSnapshot(ctx, &awsredshift.CreateClusterSnapshotInput{
		ClusterIdentifier: aws.String("ret"), SnapshotIdentifier: aws.String("s-7"),
		ManualSnapshotRetentionPeriod: aws.Int32(7),
	}); err != nil {
		t.Fatalf("CreateClusterSnapshot(7): %v", err)
	}

	if got := describeRetention(t, client, "s-7"); got != 7 {
		t.Fatalf("given retention reads back %d, want 7", got)
	}

	snaps, err := client.DescribeClusterSnapshots(ctx, &awsredshift.DescribeClusterSnapshotsInput{ClusterIdentifier: aws.String("ret")})
	if err != nil {
		t.Fatalf("DescribeClusterSnapshots: %v", err)
	}

	for _, s := range snaps.Snapshots {
		remaining := s.ManualSnapshotRemainingDays
		switch aws.ToString(s.SnapshotIdentifier) {
		case "s-default":
			if remaining != nil {
				t.Fatalf("s-default remaining days = %d, want omitted", *remaining)
			}
		case "s-7":
			if aws.ToInt32(remaining) != 7 {
				t.Fatalf("s-7 remaining days = %v, want 7", remaining)
			}
		}
	}

	for _, bad := range []int32{0, -2, 3654} {
		_, err := client.CreateClusterSnapshot(ctx, &awsredshift.CreateClusterSnapshotInput{
			ClusterIdentifier: aws.String("ret"), SnapshotIdentifier: aws.String("s-bad"),
			ManualSnapshotRetentionPeriod: aws.Int32(bad),
		})
		requireAPIErrorCode(t, err, "InvalidRetentionPeriodFault")
	}
}

func TestSDKRedshiftModifyClusterSnapshot(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()
	createRetentionCluster(t, client)

	if _, err := client.CreateClusterSnapshot(ctx, &awsredshift.CreateClusterSnapshotInput{
		ClusterIdentifier: aws.String("ret"), SnapshotIdentifier: aws.String("s1"),
	}); err != nil {
		t.Fatalf("CreateClusterSnapshot: %v", err)
	}

	out, err := client.ModifyClusterSnapshot(ctx, &awsredshift.ModifyClusterSnapshotInput{
		SnapshotIdentifier: aws.String("s1"), ManualSnapshotRetentionPeriod: aws.Int32(30),
	})
	if err != nil {
		t.Fatalf("ModifyClusterSnapshot: %v", err)
	}

	if got := aws.ToInt32(out.Snapshot.ManualSnapshotRetentionPeriod); got != 30 {
		t.Fatalf("modify response retention = %d, want 30", got)
	}

	if got := describeRetention(t, client, "s1"); got != 30 {
		t.Fatalf("modified retention reads back %d, want 30", got)
	}

	// Omitting the period leaves it as it is.
	if _, err := client.ModifyClusterSnapshot(ctx, &awsredshift.ModifyClusterSnapshotInput{
		SnapshotIdentifier: aws.String("s1"), Force: aws.Bool(true),
	}); err != nil {
		t.Fatalf("ModifyClusterSnapshot(no period): %v", err)
	}

	if got := describeRetention(t, client, "s1"); got != 30 {
		t.Fatalf("retention after no-op modify = %d, want 30", got)
	}

	if _, err := client.ModifyClusterSnapshot(ctx, &awsredshift.ModifyClusterSnapshotInput{
		SnapshotIdentifier: aws.String("s1"), ManualSnapshotRetentionPeriod: aws.Int32(-1),
	}); err != nil {
		t.Fatalf("ModifyClusterSnapshot(-1): %v", err)
	}

	if got := describeRetention(t, client, "s1"); got != -1 {
		t.Fatalf("retention after -1 = %d, want -1", got)
	}

	_, err = client.ModifyClusterSnapshot(ctx, &awsredshift.ModifyClusterSnapshotInput{
		SnapshotIdentifier: aws.String("s1"), ManualSnapshotRetentionPeriod: aws.Int32(0),
	})
	requireAPIErrorCode(t, err, "InvalidRetentionPeriodFault")

	_, err = client.ModifyClusterSnapshot(ctx, &awsredshift.ModifyClusterSnapshotInput{
		SnapshotIdentifier: aws.String("missing"), ManualSnapshotRetentionPeriod: aws.Int32(5),
	})
	requireAPIErrorCode(t, err, "ClusterSnapshotNotFound")
}
