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

// TestSDKRedshiftRetentionAndMaintenanceRoundTrip proves a user-set
// AutomatedSnapshotRetentionPeriod and PreferredMaintenanceWindow round-trip
// through create → describe and a later modify, rather than always reporting
// the create-time default. Terraform's aws_redshift_cluster reads both back
// (retention has a non-computed schema default of 1, the window is computed),
// so a value that is not stored drifts on the next plan.
func TestSDKRedshiftRetentionAndMaintenanceRoundTrip(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	// Create with explicit non-default values.
	out, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:                aws.String("retain"),
		MasterUsername:                   aws.String("admin"),
		MasterUserPassword:               aws.String("Sup3rSecret!"),
		NodeType:                         aws.String("dc2.large"),
		AutomatedSnapshotRetentionPeriod: aws.Int32(7),
		PreferredMaintenanceWindow:       aws.String("wed:03:00-wed:03:30"),
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if got := aws.ToInt32(out.Cluster.AutomatedSnapshotRetentionPeriod); got != 7 {
		t.Fatalf("create AutomatedSnapshotRetentionPeriod=%d, want 7", got)
	}
	if got := aws.ToString(out.Cluster.PreferredMaintenanceWindow); got != "wed:03:00-wed:03:30" {
		t.Fatalf("create PreferredMaintenanceWindow=%q, want wed:03:00-wed:03:30", got)
	}

	// Describe must reflect the same stored values (no drift).
	desc, err := client.DescribeClusters(ctx, &awsredshift.DescribeClustersInput{
		ClusterIdentifier: aws.String("retain"),
	})
	if err != nil {
		t.Fatalf("DescribeClusters: %v", err)
	}
	if got := aws.ToInt32(desc.Clusters[0].AutomatedSnapshotRetentionPeriod); got != 7 {
		t.Fatalf("describe AutomatedSnapshotRetentionPeriod=%d, want 7", got)
	}
	if got := aws.ToString(desc.Clusters[0].PreferredMaintenanceWindow); got != "wed:03:00-wed:03:30" {
		t.Fatalf("describe PreferredMaintenanceWindow=%q, want wed:03:00-wed:03:30", got)
	}

	// Modify both, and confirm the change is applied and persisted.
	if _, err := client.ModifyCluster(ctx, &awsredshift.ModifyClusterInput{
		ClusterIdentifier:                aws.String("retain"),
		AutomatedSnapshotRetentionPeriod: aws.Int32(14),
		PreferredMaintenanceWindow:       aws.String("mon:01:00-mon:01:30"),
	}); err != nil {
		t.Fatalf("ModifyCluster: %v", err)
	}

	desc, err = client.DescribeClusters(ctx, &awsredshift.DescribeClustersInput{
		ClusterIdentifier: aws.String("retain"),
	})
	if err != nil {
		t.Fatalf("DescribeClusters after modify: %v", err)
	}
	if got := aws.ToInt32(desc.Clusters[0].AutomatedSnapshotRetentionPeriod); got != 14 {
		t.Fatalf("after modify AutomatedSnapshotRetentionPeriod=%d, want 14", got)
	}
	if got := aws.ToString(desc.Clusters[0].PreferredMaintenanceWindow); got != "mon:01:00-mon:01:30" {
		t.Fatalf("after modify PreferredMaintenanceWindow=%q, want mon:01:00-mon:01:30", got)
	}
}

// TestSDKRedshiftDefaultRetentionAndDBName proves a create that omits
// AutomatedSnapshotRetentionPeriod and DBName reports the AWS defaults
// (retention 1, database "dev"), matching the Terraform schema so an
// unconfigured cluster does not drift.
func TestSDKRedshiftDefaultRetentionAndDBName(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	out, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String("defaults"),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("Sup3rSecret!"),
		NodeType:           aws.String("dc2.large"),
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if got := aws.ToInt32(out.Cluster.AutomatedSnapshotRetentionPeriod); got != 1 {
		t.Fatalf("default AutomatedSnapshotRetentionPeriod=%d, want 1", got)
	}
	if got := aws.ToString(out.Cluster.DBName); got != "dev" {
		t.Fatalf("default DBName=%q, want dev", got)
	}
	if aws.ToString(out.Cluster.PreferredMaintenanceWindow) == "" {
		t.Fatal("default PreferredMaintenanceWindow is empty, want an assigned window")
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
