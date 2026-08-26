package redshift_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsredshift "github.com/aws/aws-sdk-go-v2/service/redshift"
)

// TestSDKRedshiftModifyClusterResize proves ModifyCluster applies a NodeType /
// NumberOfNodes resize and that DescribeClusters reflects it — without this a
// Terraform aws_redshift_cluster changing node_type/number_of_nodes never
// converges (perpetual drift).
func TestSDKRedshiftModifyClusterResize(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String("wh"),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("Sup3rSecret!"),
		NodeType:           aws.String("dc2.large"),
		ClusterType:        aws.String("multi-node"),
		NumberOfNodes:      aws.Int32(2),
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	out, err := client.ModifyCluster(ctx, &awsredshift.ModifyClusterInput{
		ClusterIdentifier: aws.String("wh"),
		NodeType:          aws.String("ra3.xlplus"),
		NumberOfNodes:     aws.Int32(4),
	})
	if err != nil {
		t.Fatalf("ModifyCluster: %v", err)
	}

	if aws.ToString(out.Cluster.NodeType) != "ra3.xlplus" || aws.ToInt32(out.Cluster.NumberOfNodes) != 4 {
		t.Fatalf("ModifyCluster response node config = %q/%d, want ra3.xlplus/4",
			aws.ToString(out.Cluster.NodeType), aws.ToInt32(out.Cluster.NumberOfNodes))
	}

	got, err := client.DescribeClusters(ctx, &awsredshift.DescribeClustersInput{
		ClusterIdentifier: aws.String("wh"),
	})
	if err != nil {
		t.Fatalf("DescribeClusters: %v", err)
	}

	if aws.ToString(got.Clusters[0].NodeType) != "ra3.xlplus" || aws.ToInt32(got.Clusters[0].NumberOfNodes) != 4 {
		t.Fatalf("after resize DescribeClusters node config = %q/%d, want ra3.xlplus/4",
			aws.ToString(got.Clusters[0].NodeType), aws.ToInt32(got.Clusters[0].NumberOfNodes))
	}
}

// TestSDKRedshiftModifyClusterSingleNode proves ClusterType single-node forces
// NumberOfNodes to 1 on resize.
func TestSDKRedshiftModifyClusterSingleNode(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String("wh2"),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("Sup3rSecret!"),
		NodeType:           aws.String("dc2.large"),
		ClusterType:        aws.String("multi-node"),
		NumberOfNodes:      aws.Int32(3),
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	out, err := client.ModifyCluster(ctx, &awsredshift.ModifyClusterInput{
		ClusterIdentifier: aws.String("wh2"),
		NodeType:          aws.String("dc2.large"),
		ClusterType:       aws.String("single-node"),
	})
	if err != nil {
		t.Fatalf("ModifyCluster: %v", err)
	}

	if aws.ToInt32(out.Cluster.NumberOfNodes) != 1 {
		t.Fatalf("single-node resize NumberOfNodes = %d, want 1", aws.ToInt32(out.Cluster.NumberOfNodes))
	}
}
