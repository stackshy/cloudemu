package rds_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
)

func TestSDKRDSCopyAndPITR(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	// Instance + snapshot, then copy the snapshot.
	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("db"),
		Engine:               aws.String("postgres"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(40),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	if _, err := client.CreateDBSnapshot(ctx, &awsrds.CreateDBSnapshotInput{
		DBInstanceIdentifier: aws.String("db"),
		DBSnapshotIdentifier: aws.String("snap"),
	}); err != nil {
		t.Fatalf("CreateDBSnapshot: %v", err)
	}

	copied, err := client.CopyDBSnapshot(ctx, &awsrds.CopyDBSnapshotInput{
		SourceDBSnapshotIdentifier: aws.String("snap"),
		TargetDBSnapshotIdentifier: aws.String("snap-copy"),
	})
	if err != nil {
		t.Fatalf("CopyDBSnapshot: %v", err)
	}

	if aws.ToString(copied.DBSnapshot.DBSnapshotIdentifier) != "snap-copy" {
		t.Fatalf("copy id = %q, want snap-copy", aws.ToString(copied.DBSnapshot.DBSnapshotIdentifier))
	}

	// Point-in-time restore of the instance.
	restored, err := client.RestoreDBInstanceToPointInTime(ctx, &awsrds.RestoreDBInstanceToPointInTimeInput{
		SourceDBInstanceIdentifier: aws.String("db"),
		TargetDBInstanceIdentifier: aws.String("db-pitr"),
		UseLatestRestorableTime:    aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("RestoreDBInstanceToPointInTime: %v", err)
	}

	if aws.ToString(restored.DBInstance.Engine) != "postgres" {
		t.Fatalf("restored engine = %q, want postgres", aws.ToString(restored.DBInstance.Engine))
	}

	// Cluster + cluster snapshot copy + cluster PITR.
	if _, err := client.CreateDBCluster(ctx, &awsrds.CreateDBClusterInput{
		DBClusterIdentifier: aws.String("cl"),
		Engine:              aws.String("aurora-mysql"),
	}); err != nil {
		t.Fatalf("CreateDBCluster: %v", err)
	}

	if _, err := client.CreateDBClusterSnapshot(ctx, &awsrds.CreateDBClusterSnapshotInput{
		DBClusterIdentifier:         aws.String("cl"),
		DBClusterSnapshotIdentifier: aws.String("csnap"),
	}); err != nil {
		t.Fatalf("CreateDBClusterSnapshot: %v", err)
	}

	if _, err := client.CopyDBClusterSnapshot(ctx, &awsrds.CopyDBClusterSnapshotInput{
		SourceDBClusterSnapshotIdentifier: aws.String("csnap"),
		TargetDBClusterSnapshotIdentifier: aws.String("csnap-copy"),
	}); err != nil {
		t.Fatalf("CopyDBClusterSnapshot: %v", err)
	}

	clRestored, err := client.RestoreDBClusterToPointInTime(ctx, &awsrds.RestoreDBClusterToPointInTimeInput{
		SourceDBClusterIdentifier: aws.String("cl"),
		DBClusterIdentifier:       aws.String("cl-pitr"),
		UseLatestRestorableTime:   aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("RestoreDBClusterToPointInTime: %v", err)
	}

	if aws.ToString(clRestored.DBCluster.DBClusterIdentifier) != "cl-pitr" {
		t.Fatalf("restored cluster id = %q, want cl-pitr", aws.ToString(clRestored.DBCluster.DBClusterIdentifier))
	}
}
