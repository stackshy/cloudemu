package redshift_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsredshift "github.com/aws/aws-sdk-go-v2/service/redshift"
)

// TestSDKRedshiftRestorePreservesConfig proves a cluster restored from a snapshot
// carries the snapshot's node config and the source cluster's admin user / DB
// name — without it a restored cluster reads back with empty NodeType, zero
// NumberOfNodes and no MasterUsername/DBName, so IaC sees the wrong shape.
func TestSDKRedshiftRestorePreservesConfig(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String("src"),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("Sup3rSecret!"),
		NodeType:           aws.String("ra3.xlplus"),
		ClusterType:        aws.String("multi-node"),
		NumberOfNodes:      aws.Int32(3),
		DBName:             aws.String("dev"),
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := client.CreateClusterSnapshot(ctx, &awsredshift.CreateClusterSnapshotInput{
		SnapshotIdentifier: aws.String("snap1"),
		ClusterIdentifier:  aws.String("src"),
	}); err != nil {
		t.Fatalf("CreateClusterSnapshot: %v", err)
	}

	restored, err := client.RestoreFromClusterSnapshot(ctx, &awsredshift.RestoreFromClusterSnapshotInput{
		ClusterIdentifier:  aws.String("restored"),
		SnapshotIdentifier: aws.String("snap1"),
	})
	if err != nil {
		t.Fatalf("RestoreFromClusterSnapshot: %v", err)
	}

	c := restored.Cluster
	if aws.ToString(c.NodeType) != "ra3.xlplus" || aws.ToInt32(c.NumberOfNodes) != 3 ||
		aws.ToString(c.MasterUsername) != "admin" || aws.ToString(c.DBName) != "dev" {
		t.Fatalf("restored config = node %q/%d user %q db %q, want ra3.xlplus/3/admin/dev",
			aws.ToString(c.NodeType), aws.ToInt32(c.NumberOfNodes),
			aws.ToString(c.MasterUsername), aws.ToString(c.DBName))
	}

	// The synthesized ClusterNodes list should be present (NumberOfNodes > 0).
	got, err := client.DescribeClusters(ctx, &awsredshift.DescribeClustersInput{
		ClusterIdentifier: aws.String("restored"),
	})
	if err != nil {
		t.Fatalf("DescribeClusters: %v", err)
	}

	if len(got.Clusters[0].ClusterNodes) == 0 {
		t.Fatal("restored cluster has no ClusterNodes (NumberOfNodes not preserved)")
	}
}
