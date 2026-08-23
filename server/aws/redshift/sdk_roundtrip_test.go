package redshift_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
	awsredshift "github.com/aws/aws-sdk-go-v2/service/redshift"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newSDKClient(t *testing.T) *awsredshift.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		Redshift: cloud.Redshift,
		// EC2 + RDS also wired so we exercise the dispatch precedence: a
		// request for Redshift must claim the body before EC2 sees it, and
		// the RDS handler must not shadow Redshift either.
		EC2: cloud.EC2,
		RDS: cloud.RDS,
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsredshift.NewFromConfig(cfg, func(o *awsredshift.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKRedshiftCreateDescribeCluster(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	out, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String("warehouse"),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("Sup3rSecret!"),
		NodeType:           aws.String("ra3.xlplus"),
		DBName:             aws.String("dev"),
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if aws.ToString(out.Cluster.ClusterIdentifier) != "warehouse" {
		t.Fatalf("got id %q, want warehouse", aws.ToString(out.Cluster.ClusterIdentifier))
	}

	if aws.ToString(out.Cluster.ClusterStatus) != "available" {
		t.Fatalf("got status %q, want available", aws.ToString(out.Cluster.ClusterStatus))
	}

	got, err := client.DescribeClusters(ctx, &awsredshift.DescribeClustersInput{
		ClusterIdentifier: aws.String("warehouse"),
	})
	if err != nil {
		t.Fatalf("DescribeClusters: %v", err)
	}

	if len(got.Clusters) != 1 {
		t.Fatalf("got %d clusters, want 1", len(got.Clusters))
	}

	if got.Clusters[0].Endpoint == nil || aws.ToString(got.Clusters[0].Endpoint.Address) == "" {
		t.Fatal("expected endpoint to be set")
	}
}

// TestSDKRedshiftParameterAndSubnetGroups is a regression guard for issue
// #319: CreateClusterParameterGroup / CreateClusterSubnetGroup returned
// InvalidAction, blocking IaC that provisions a warehouse with a custom group.
func TestSDKRedshiftParameterAndSubnetGroups(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	pg, err := client.CreateClusterParameterGroup(ctx, &awsredshift.CreateClusterParameterGroupInput{
		ParameterGroupName:   aws.String("pg1"),
		ParameterGroupFamily: aws.String("redshift-1.0"),
		Description:          aws.String("my pg"),
	})
	if err != nil {
		t.Fatalf("CreateClusterParameterGroup: %v", err)
	}

	if aws.ToString(pg.ClusterParameterGroup.ParameterGroupName) != "pg1" ||
		aws.ToString(pg.ClusterParameterGroup.ParameterGroupFamily) != "redshift-1.0" {
		t.Fatalf("parameter group = %+v", pg.ClusterParameterGroup)
	}

	sg, err := client.CreateClusterSubnetGroup(ctx, &awsredshift.CreateClusterSubnetGroupInput{
		ClusterSubnetGroupName: aws.String("sg1"),
		Description:            aws.String("my sg"),
		SubnetIds:              []string{"subnet-1", "subnet-2"},
	})
	if err != nil {
		t.Fatalf("CreateClusterSubnetGroup: %v", err)
	}

	if aws.ToString(sg.ClusterSubnetGroup.ClusterSubnetGroupName) != "sg1" {
		t.Fatalf("subnet group = %+v", sg.ClusterSubnetGroup)
	}

	// Read the groups back — you must be able to describe what you create.
	dpg, err := client.DescribeClusterParameterGroups(ctx, &awsredshift.DescribeClusterParameterGroupsInput{
		ParameterGroupName: aws.String("pg1"),
	})
	if err != nil {
		t.Fatalf("DescribeClusterParameterGroups: %v", err)
	}

	if len(dpg.ParameterGroups) != 1 ||
		aws.ToString(dpg.ParameterGroups[0].ParameterGroupFamily) != "redshift-1.0" {
		t.Fatalf("describe parameter groups = %+v", dpg.ParameterGroups)
	}

	dsg, err := client.DescribeClusterSubnetGroups(ctx, &awsredshift.DescribeClusterSubnetGroupsInput{
		ClusterSubnetGroupName: aws.String("sg1"),
	})
	if err != nil {
		t.Fatalf("DescribeClusterSubnetGroups: %v", err)
	}

	if len(dsg.ClusterSubnetGroups) != 1 ||
		aws.ToString(dsg.ClusterSubnetGroups[0].ClusterSubnetGroupName) != "sg1" {
		t.Fatalf("describe subnet groups = %+v", dsg.ClusterSubnetGroups)
	}

	// Clean them up.
	if _, err := client.DeleteClusterParameterGroup(ctx, &awsredshift.DeleteClusterParameterGroupInput{
		ParameterGroupName: aws.String("pg1"),
	}); err != nil {
		t.Fatalf("DeleteClusterParameterGroup: %v", err)
	}

	if _, err := client.DeleteClusterSubnetGroup(ctx, &awsredshift.DeleteClusterSubnetGroupInput{
		ClusterSubnetGroupName: aws.String("sg1"),
	}); err != nil {
		t.Fatalf("DeleteClusterSubnetGroup: %v", err)
	}

	// A deleted parameter group is gone.
	if _, err := client.DescribeClusterParameterGroups(ctx, &awsredshift.DescribeClusterParameterGroupsInput{
		ParameterGroupName: aws.String("pg1"),
	}); err == nil {
		t.Fatal("expected error describing a deleted parameter group")
	}
}

func TestSDKRedshiftClusterLifecycle(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String("life"),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("password1"),
		NodeType:           aws.String("dc2.large"),
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := client.RebootCluster(ctx, &awsredshift.RebootClusterInput{
		ClusterIdentifier: aws.String("life"),
	}); err != nil {
		t.Fatalf("RebootCluster: %v", err)
	}

	if _, err := client.ModifyCluster(ctx, &awsredshift.ModifyClusterInput{
		ClusterIdentifier: aws.String("life"),
		ClusterVersion:    aws.String("1.0.32"),
	}); err != nil {
		t.Fatalf("ModifyCluster: %v", err)
	}

	got, err := client.DescribeClusters(ctx, &awsredshift.DescribeClustersInput{
		ClusterIdentifier: aws.String("life"),
	})
	if err != nil {
		t.Fatalf("DescribeClusters: %v", err)
	}

	if aws.ToString(got.Clusters[0].ClusterVersion) != "1.0.32" {
		t.Fatalf("modify did not apply, got version=%q",
			aws.ToString(got.Clusters[0].ClusterVersion))
	}

	if _, err := client.DeleteCluster(ctx, &awsredshift.DeleteClusterInput{
		ClusterIdentifier:        aws.String("life"),
		SkipFinalClusterSnapshot: aws.Bool(true),
	}); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}
}

func TestSDKRedshiftSnapshotAndRestore(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String("snapsrc"),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("Sup3rSecret!"),
		NodeType:           aws.String("ra3.xlplus"),
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	snap, err := client.CreateClusterSnapshot(ctx, &awsredshift.CreateClusterSnapshotInput{
		SnapshotIdentifier: aws.String("snap1"),
		ClusterIdentifier:  aws.String("snapsrc"),
	})
	if err != nil {
		t.Fatalf("CreateClusterSnapshot: %v", err)
	}

	if aws.ToString(snap.Snapshot.SnapshotIdentifier) != "snap1" {
		t.Fatalf("got snap id %q, want snap1",
			aws.ToString(snap.Snapshot.SnapshotIdentifier))
	}

	list, err := client.DescribeClusterSnapshots(ctx, &awsredshift.DescribeClusterSnapshotsInput{
		SnapshotIdentifier: aws.String("snap1"),
	})
	if err != nil {
		t.Fatalf("DescribeClusterSnapshots: %v", err)
	}

	if len(list.Snapshots) != 1 {
		t.Fatalf("got %d snapshots, want 1", len(list.Snapshots))
	}

	restored, err := client.RestoreFromClusterSnapshot(ctx,
		&awsredshift.RestoreFromClusterSnapshotInput{
			ClusterIdentifier:  aws.String("restored1"),
			SnapshotIdentifier: aws.String("snap1"),
		})
	if err != nil {
		t.Fatalf("RestoreFromClusterSnapshot: %v", err)
	}

	if aws.ToString(restored.Cluster.ClusterIdentifier) != "restored1" {
		t.Fatalf("got restore id %q, want restored1",
			aws.ToString(restored.Cluster.ClusterIdentifier))
	}

	if _, err := client.DeleteClusterSnapshot(ctx, &awsredshift.DeleteClusterSnapshotInput{
		SnapshotIdentifier: aws.String("snap1"),
	}); err != nil {
		t.Fatalf("DeleteClusterSnapshot: %v", err)
	}
}

// Sanity check: when Redshift, RDS and EC2 are wired together, requests still
// route to the right handler — Redshift's Matches must reject non-Redshift
// actions despite parsing the form first, and RDS likewise must not shadow
// Redshift cluster verbs.
func TestSDKRedshiftRoutingDoesNotShadowOthers(t *testing.T) {
	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		Redshift: cloud.Redshift,
		RDS:      cloud.RDS,
		EC2:      cloud.EC2,
		VPC:      cloud.VPC,
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	// EC2 SDK against the combined server.
	ec2Client := awsec2.NewFromConfig(cfg, func(o *awsec2.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})

	if _, err := ec2Client.RunInstances(context.Background(), &awsec2.RunInstancesInput{
		ImageId:  aws.String("ami-1"),
		MinCount: aws.Int32(1),
		MaxCount: aws.Int32(1),
	}); err != nil {
		t.Fatalf("EC2 RunInstances through combined server: %v", err)
	}

	// RDS SDK against the same combined server — its CreateDBInstance must
	// reach the RDS handler, not be claimed by Redshift.
	rdsClient := awsrds.NewFromConfig(cfg, func(o *awsrds.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})

	if _, err := rdsClient.CreateDBInstance(context.Background(), &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("rds1"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
	}); err != nil {
		t.Fatalf("RDS CreateDBInstance through combined server: %v", err)
	}
}
