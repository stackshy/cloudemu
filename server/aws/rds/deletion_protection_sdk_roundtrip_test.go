package rds_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
	smithy "github.com/aws/smithy-go"
)

// TestSDKRDSDeleteInstanceDeletionProtection asserts that a deletion-protected
// DB instance cannot be deleted: real RDS rejects with InvalidParameterCombination
// and leaves the instance intact.
func TestSDKRDSDeleteInstanceDeletionProtection(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("protected-db"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	if _, err := client.ModifyDBInstance(ctx, &awsrds.ModifyDBInstanceInput{
		DBInstanceIdentifier: aws.String("protected-db"),
		DeletionProtection:   aws.Bool(true),
		ApplyImmediately:     aws.Bool(true),
	}); err != nil {
		t.Fatalf("ModifyDBInstance: %v", err)
	}

	desc, err := client.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String("protected-db"),
	})
	if err != nil {
		t.Fatalf("DescribeDBInstances: %v", err)
	}

	if !aws.ToBool(desc.DBInstances[0].DeletionProtection) {
		t.Fatal("expected DeletionProtection=true after modify")
	}

	_, err = client.DeleteDBInstance(ctx, &awsrds.DeleteDBInstanceInput{
		DBInstanceIdentifier: aws.String("protected-db"),
		SkipFinalSnapshot:    aws.Bool(true),
	})
	if err == nil {
		t.Fatal("delete protected instance: want InvalidParameterCombination, got nil")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterCombination" {
		t.Fatalf("delete protected instance: got %v, want InvalidParameterCombination", err)
	}

	// The instance must still exist.
	if _, err := client.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String("protected-db"),
	}); err != nil {
		t.Fatalf("instance was deleted despite protection: %v", err)
	}

	// Disabling protection lets the delete succeed.
	if _, err := client.ModifyDBInstance(ctx, &awsrds.ModifyDBInstanceInput{
		DBInstanceIdentifier: aws.String("protected-db"),
		DeletionProtection:   aws.Bool(false),
		ApplyImmediately:     aws.Bool(true),
	}); err != nil {
		t.Fatalf("ModifyDBInstance disable: %v", err)
	}

	if _, err := client.DeleteDBInstance(ctx, &awsrds.DeleteDBInstanceInput{
		DBInstanceIdentifier: aws.String("protected-db"),
		SkipFinalSnapshot:    aws.Bool(true),
	}); err != nil {
		t.Fatalf("DeleteDBInstance after disabling protection: %v", err)
	}
}

// TestSDKRDSDeleteClusterDeletionProtection asserts DeletionProtection round-trips
// on a DB cluster and blocks deletion with InvalidParameterCombination.
func TestSDKRDSDeleteClusterDeletionProtection(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBCluster(ctx, &awsrds.CreateDBClusterInput{
		DBClusterIdentifier: aws.String("aud-cluster"),
		Engine:              aws.String("aurora-mysql"),
		MasterUsername:      aws.String("admin"),
		MasterUserPassword:  aws.String("supersecret"),
	}); err != nil {
		t.Fatalf("CreateDBCluster: %v", err)
	}

	if _, err := client.ModifyDBCluster(ctx, &awsrds.ModifyDBClusterInput{
		DBClusterIdentifier: aws.String("aud-cluster"),
		DeletionProtection:  aws.Bool(true),
		ApplyImmediately:    aws.Bool(true),
	}); err != nil {
		t.Fatalf("ModifyDBCluster: %v", err)
	}

	desc, err := client.DescribeDBClusters(ctx, &awsrds.DescribeDBClustersInput{
		DBClusterIdentifier: aws.String("aud-cluster"),
	})
	if err != nil {
		t.Fatalf("DescribeDBClusters: %v", err)
	}

	if !aws.ToBool(desc.DBClusters[0].DeletionProtection) {
		t.Fatal("expected cluster DeletionProtection=true after modify")
	}

	_, err = client.DeleteDBCluster(ctx, &awsrds.DeleteDBClusterInput{
		DBClusterIdentifier: aws.String("aud-cluster"),
		SkipFinalSnapshot:   aws.Bool(true),
	})
	if err == nil {
		t.Fatal("delete protected cluster: want InvalidParameterCombination, got nil")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterCombination" {
		t.Fatalf("delete protected cluster: got %v, want InvalidParameterCombination", err)
	}

	// Cluster must still exist.
	if _, err := client.DescribeDBClusters(ctx, &awsrds.DescribeDBClustersInput{
		DBClusterIdentifier: aws.String("aud-cluster"),
	}); err != nil {
		t.Fatalf("cluster was deleted despite protection: %v", err)
	}
}

// TestSDKRDSDeleteClusterFinalSnapshotRequired asserts that DeleteDBCluster with
// neither SkipFinalSnapshot nor FinalDBSnapshotIdentifier fails with
// InvalidParameterCombination, mirroring DeleteDBInstance.
func TestSDKRDSDeleteClusterFinalSnapshotRequired(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBCluster(ctx, &awsrds.CreateDBClusterInput{
		DBClusterIdentifier: aws.String("aud-cluster2"),
		Engine:              aws.String("aurora-mysql"),
		MasterUsername:      aws.String("admin"),
		MasterUserPassword:  aws.String("supersecret"),
	}); err != nil {
		t.Fatalf("CreateDBCluster: %v", err)
	}

	_, err := client.DeleteDBCluster(ctx, &awsrds.DeleteDBClusterInput{
		DBClusterIdentifier: aws.String("aud-cluster2"),
	})
	if err == nil {
		t.Fatal("delete cluster without skip or final id: want InvalidParameterCombination, got nil")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterCombination" {
		t.Fatalf("delete cluster without skip or final id: got %v, want InvalidParameterCombination", err)
	}

	// Providing a final snapshot id creates the snapshot before deleting.
	if _, err := client.DeleteDBCluster(ctx, &awsrds.DeleteDBClusterInput{
		DBClusterIdentifier:       aws.String("aud-cluster2"),
		FinalDBSnapshotIdentifier: aws.String("aud-cluster2-final"),
	}); err != nil {
		t.Fatalf("DeleteDBCluster with final snapshot: %v", err)
	}

	snaps, err := client.DescribeDBClusterSnapshots(ctx, &awsrds.DescribeDBClusterSnapshotsInput{
		DBClusterSnapshotIdentifier: aws.String("aud-cluster2-final"),
	})
	if err != nil {
		t.Fatalf("DescribeDBClusterSnapshots: %v", err)
	}

	if len(snaps.DBClusterSnapshots) != 1 {
		t.Fatalf("got %d final cluster snapshots, want 1", len(snaps.DBClusterSnapshots))
	}
}
