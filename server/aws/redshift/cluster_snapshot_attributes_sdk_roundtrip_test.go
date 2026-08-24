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
