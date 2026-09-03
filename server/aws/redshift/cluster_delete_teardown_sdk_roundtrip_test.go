package redshift_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsredshift "github.com/aws/aws-sdk-go-v2/service/redshift"
	"github.com/aws/smithy-go"
)

// createWarehouse creates a minimal available cluster the teardown tests act on.
func createWarehouse(t *testing.T, client *awsredshift.Client, id string) {
	t.Helper()

	if _, err := client.CreateCluster(context.Background(), &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String(id),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("Sup3rSecret!"),
		NodeType:           aws.String("ra3.xlplus"),
		DBName:             aws.String("dev"),
	}); err != nil {
		t.Fatalf("CreateCluster(%s): %v", id, err)
	}
}

func errorCode(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode()
	}

	return ""
}

// TestSDKRedshiftDeleteClusterFinalSnapshot proves DeleteCluster honors the
// final-snapshot flags exactly as real Redshift does: a final snapshot is
// required (and taken, visible as a manual snapshot) unless the caller opts out
// with SkipFinalClusterSnapshot.
func TestSDKRedshiftDeleteClusterFinalSnapshot(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	// (1) SkipFinalClusterSnapshot=false + a FinalClusterSnapshotIdentifier →
	// the cluster is removed and a manual snapshot appears.
	createWarehouse(t, client, "wh-final")

	if _, err := client.DeleteCluster(ctx, &awsredshift.DeleteClusterInput{
		ClusterIdentifier:              aws.String("wh-final"),
		SkipFinalClusterSnapshot:       aws.Bool(false),
		FinalClusterSnapshotIdentifier: aws.String("wh-final-snap"),
	}); err != nil {
		t.Fatalf("DeleteCluster with final snapshot: %v", err)
	}

	snaps, err := client.DescribeClusterSnapshots(ctx, &awsredshift.DescribeClusterSnapshotsInput{
		SnapshotIdentifier: aws.String("wh-final-snap"),
	})
	if err != nil {
		t.Fatalf("DescribeClusterSnapshots: %v", err)
	}

	if len(snaps.Snapshots) != 1 {
		t.Fatalf("got %d snapshots, want 1 final snapshot", len(snaps.Snapshots))
	}

	if aws.ToString(snaps.Snapshots[0].SnapshotType) != "manual" {
		t.Fatalf("final snapshot type = %q, want manual", aws.ToString(snaps.Snapshots[0].SnapshotType))
	}

	if _, err := client.DescribeClusters(ctx, &awsredshift.DescribeClustersInput{
		ClusterIdentifier: aws.String("wh-final"),
	}); errorCode(err) != "ClusterNotFound" {
		t.Fatalf("DescribeClusters after delete = %v, want ClusterNotFound", err)
	}

	// (2) SkipFinalClusterSnapshot=false + NO identifier → rejected, cluster
	// survives, no snapshot written.
	createWarehouse(t, client, "wh-noid")

	_, err = client.DeleteCluster(ctx, &awsredshift.DeleteClusterInput{
		ClusterIdentifier:        aws.String("wh-noid"),
		SkipFinalClusterSnapshot: aws.Bool(false),
	})
	if errorCode(err) != "InvalidParameterCombination" {
		t.Fatalf("DeleteCluster without identifier = %v, want InvalidParameterCombination", err)
	}

	if got, derr := client.DescribeClusters(ctx, &awsredshift.DescribeClustersInput{
		ClusterIdentifier: aws.String("wh-noid"),
	}); derr != nil || len(got.Clusters) != 1 {
		t.Fatalf("cluster should survive a rejected delete: %+v, err %v", got, derr)
	}

	// (3) SkipFinalClusterSnapshot=true → deleted, no new snapshot.
	createWarehouse(t, client, "wh-skip")

	if _, err := client.DeleteCluster(ctx, &awsredshift.DeleteClusterInput{
		ClusterIdentifier:        aws.String("wh-skip"),
		SkipFinalClusterSnapshot: aws.Bool(true),
	}); err != nil {
		t.Fatalf("DeleteCluster skip final snapshot: %v", err)
	}

	all, err := client.DescribeClusterSnapshots(ctx, &awsredshift.DescribeClusterSnapshotsInput{})
	if err != nil {
		t.Fatalf("DescribeClusterSnapshots (all): %v", err)
	}

	// Only the "wh-final-snap" from case (1) should exist; skip left none behind.
	if len(all.Snapshots) != 1 {
		t.Fatalf("got %d snapshots after skip-final delete, want 1", len(all.Snapshots))
	}
}

// TestSDKRedshiftGroupInUseGuards proves a subnet group / parameter group
// referenced by a live cluster cannot be torn down (real Redshift in-use
// faults), and that the delete succeeds once the cluster is gone.
func TestSDKRedshiftGroupInUseGuards(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateClusterSubnetGroup(ctx, &awsredshift.CreateClusterSubnetGroupInput{
		ClusterSubnetGroupName: aws.String("sng"),
		Description:            aws.String("desc"),
		SubnetIds:              []string{"subnet-1"},
	}); err != nil {
		t.Fatalf("CreateClusterSubnetGroup: %v", err)
	}

	if _, err := client.CreateClusterParameterGroup(ctx, &awsredshift.CreateClusterParameterGroupInput{
		ParameterGroupName:   aws.String("pg"),
		ParameterGroupFamily: aws.String("redshift-1.0"),
		Description:          aws.String("desc"),
	}); err != nil {
		t.Fatalf("CreateClusterParameterGroup: %v", err)
	}

	if _, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:         aws.String("wh"),
		MasterUsername:            aws.String("admin"),
		MasterUserPassword:        aws.String("Sup3rSecret!"),
		NodeType:                  aws.String("ra3.xlplus"),
		ClusterSubnetGroupName:    aws.String("sng"),
		ClusterParameterGroupName: aws.String("pg"),
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	// In use → each delete faults with its own code; the group survives.
	_, err := client.DeleteClusterSubnetGroup(ctx, &awsredshift.DeleteClusterSubnetGroupInput{
		ClusterSubnetGroupName: aws.String("sng"),
	})
	if errorCode(err) != "ClusterSubnetGroupInUseFault" {
		t.Fatalf("DeleteClusterSubnetGroup in use = %v, want ClusterSubnetGroupInUseFault", err)
	}

	_, err = client.DeleteClusterParameterGroup(ctx, &awsredshift.DeleteClusterParameterGroupInput{
		ParameterGroupName: aws.String("pg"),
	})
	if errorCode(err) != "InvalidClusterParameterGroupStateFault" {
		t.Fatalf("DeleteClusterParameterGroup in use = %v, want InvalidClusterParameterGroupStateFault", err)
	}

	if _, err := client.DescribeClusterSubnetGroups(ctx, &awsredshift.DescribeClusterSubnetGroupsInput{
		ClusterSubnetGroupName: aws.String("sng"),
	}); err != nil {
		t.Fatalf("subnet group should still exist: %v", err)
	}

	// Drop the cluster (skip its own final snapshot), then the groups delete.
	if _, err := client.DeleteCluster(ctx, &awsredshift.DeleteClusterInput{
		ClusterIdentifier:        aws.String("wh"),
		SkipFinalClusterSnapshot: aws.Bool(true),
	}); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	if _, err := client.DeleteClusterSubnetGroup(ctx, &awsredshift.DeleteClusterSubnetGroupInput{
		ClusterSubnetGroupName: aws.String("sng"),
	}); err != nil {
		t.Fatalf("DeleteClusterSubnetGroup after cluster gone: %v", err)
	}

	if _, err := client.DeleteClusterParameterGroup(ctx, &awsredshift.DeleteClusterParameterGroupInput{
		ParameterGroupName: aws.String("pg"),
	}); err != nil {
		t.Fatalf("DeleteClusterParameterGroup after cluster gone: %v", err)
	}
}
