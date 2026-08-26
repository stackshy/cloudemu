package redshift_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsredshift "github.com/aws/aws-sdk-go-v2/service/redshift"
)

// TestSDKRedshiftSnapshotAttributes asserts a cluster snapshot inherits the
// source cluster's NodeType/NumberOfNodes/Encrypted and reports a backup size.
func TestSDKRedshiftSnapshotAttributes(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String("snapattrs"),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("Sup3rSecret!"),
		NodeType:           aws.String("ra3.xlplus"),
		NumberOfNodes:      aws.Int32(3),
		Encrypted:          aws.Bool(true),
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	out, err := client.CreateClusterSnapshot(ctx, &awsredshift.CreateClusterSnapshotInput{
		SnapshotIdentifier: aws.String("snap-attrs"),
		ClusterIdentifier:  aws.String("snapattrs"),
	})
	if err != nil {
		t.Fatalf("CreateClusterSnapshot: %v", err)
	}

	snap := out.Snapshot

	if aws.ToString(snap.NodeType) != "ra3.xlplus" {
		t.Fatalf("snapshot NodeType=%q, want ra3.xlplus", aws.ToString(snap.NodeType))
	}

	if aws.ToInt32(snap.NumberOfNodes) != 3 {
		t.Fatalf("snapshot NumberOfNodes=%d, want 3", aws.ToInt32(snap.NumberOfNodes))
	}

	if !aws.ToBool(snap.Encrypted) {
		t.Fatal("snapshot Encrypted=false, want true")
	}

	if snap.TotalBackupSizeInMegaBytes == nil || aws.ToFloat64(snap.TotalBackupSizeInMegaBytes) <= 0 {
		t.Fatalf("snapshot TotalBackupSizeInMegaBytes=%v, want a positive size", snap.TotalBackupSizeInMegaBytes)
	}
}

// TestSDKRedshiftSnapshotPreservesKmsKeyId proves the source cluster's KmsKeyId
// is captured on the snapshot, reported on DescribeClusterSnapshots, and carried
// onto a restored cluster — the encryption key survives snapshot/restore so
// aws_redshift_cluster / snapshot Terraform does not drift.
func TestSDKRedshiftSnapshotPreservesKmsKeyId(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const kmsARN = "arn:aws:kms:us-east-1:123456789012:key/enc-key-1"

	if _, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String("kmssrc"),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("Sup3rSecret!"),
		NodeType:           aws.String("ra3.xlplus"),
		Encrypted:          aws.Bool(true),
		KmsKeyId:           aws.String(kmsARN),
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	snapOut, err := client.CreateClusterSnapshot(ctx, &awsredshift.CreateClusterSnapshotInput{
		SnapshotIdentifier: aws.String("kms-snap"),
		ClusterIdentifier:  aws.String("kmssrc"),
	})
	if err != nil {
		t.Fatalf("CreateClusterSnapshot: %v", err)
	}

	if !aws.ToBool(snapOut.Snapshot.Encrypted) || aws.ToString(snapOut.Snapshot.KmsKeyId) != kmsARN {
		t.Fatalf("snapshot Encrypted/KmsKeyId = %v/%q, want true/%q",
			aws.ToBool(snapOut.Snapshot.Encrypted), aws.ToString(snapOut.Snapshot.KmsKeyId), kmsARN)
	}

	list, err := client.DescribeClusterSnapshots(ctx, &awsredshift.DescribeClusterSnapshotsInput{
		SnapshotIdentifier: aws.String("kms-snap"),
	})
	if err != nil {
		t.Fatalf("DescribeClusterSnapshots: %v", err)
	}

	if len(list.Snapshots) != 1 || aws.ToString(list.Snapshots[0].KmsKeyId) != kmsARN {
		t.Fatalf("describe snapshot KmsKeyId = %q, want %q",
			aws.ToString(list.Snapshots[0].KmsKeyId), kmsARN)
	}

	restored, err := client.RestoreFromClusterSnapshot(ctx, &awsredshift.RestoreFromClusterSnapshotInput{
		ClusterIdentifier:  aws.String("kms-restored"),
		SnapshotIdentifier: aws.String("kms-snap"),
	})
	if err != nil {
		t.Fatalf("RestoreFromClusterSnapshot: %v", err)
	}

	if !aws.ToBool(restored.Cluster.Encrypted) || aws.ToString(restored.Cluster.KmsKeyId) != kmsARN {
		t.Fatalf("restored Encrypted/KmsKeyId = %v/%q, want true/%q",
			aws.ToBool(restored.Cluster.Encrypted), aws.ToString(restored.Cluster.KmsKeyId), kmsARN)
	}

	got, err := client.DescribeClusters(ctx, &awsredshift.DescribeClustersInput{
		ClusterIdentifier: aws.String("kms-restored"),
	})
	if err != nil {
		t.Fatalf("DescribeClusters: %v", err)
	}

	if len(got.Clusters) != 1 || aws.ToString(got.Clusters[0].KmsKeyId) != kmsARN {
		t.Fatalf("restored cluster describe KmsKeyId = %q, want %q",
			aws.ToString(got.Clusters[0].KmsKeyId), kmsARN)
	}
}
