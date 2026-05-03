package rds_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsdocdb "github.com/aws/aws-sdk-go-v2/service/docdb"

	"github.com/stackshy/cloudemu"
	awsserver "github.com/stackshy/cloudemu/server/aws"
)

// DocumentDB ships its own aws-sdk-go-v2 client but speaks the exact same
// query-protocol wire shape as RDS — only the Engine value ("docdb") and
// default port (27017) differ. The tests below drive the real DocDB client
// against the cloudemu RDS handler to prove the shared surface holds.

func newDocDBSDKClient(t *testing.T) *awsdocdb.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		RDS: cloud.RDS,
		EC2: cloud.EC2,
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

	return awsdocdb.NewFromConfig(cfg, func(o *awsdocdb.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKDocDBClusterCRUD(t *testing.T) {
	client := newDocDBSDKClient(t)
	ctx := context.Background()

	out, err := client.CreateDBCluster(ctx, &awsdocdb.CreateDBClusterInput{
		DBClusterIdentifier: aws.String("doc1"),
		Engine:              aws.String("docdb"),
		MasterUsername:      aws.String("admin"),
		MasterUserPassword:  aws.String("supersecret"),
	})
	if err != nil {
		t.Fatalf("CreateDBCluster: %v", err)
	}

	if aws.ToString(out.DBCluster.DBClusterIdentifier) != "doc1" {
		t.Fatalf("got cluster id %q, want doc1",
			aws.ToString(out.DBCluster.DBClusterIdentifier))
	}

	if aws.ToString(out.DBCluster.Engine) != "docdb" {
		t.Fatalf("got engine %q, want docdb", aws.ToString(out.DBCluster.Engine))
	}

	if out.DBCluster.Port == nil || *out.DBCluster.Port != 27017 {
		t.Fatalf("got port %v, want 27017", out.DBCluster.Port)
	}

	desc, err := client.DescribeDBClusters(ctx, &awsdocdb.DescribeDBClustersInput{
		DBClusterIdentifier: aws.String("doc1"),
	})
	if err != nil {
		t.Fatalf("DescribeDBClusters: %v", err)
	}

	if len(desc.DBClusters) != 1 {
		t.Fatalf("got %d clusters, want 1", len(desc.DBClusters))
	}

	if _, err := client.ModifyDBCluster(ctx, &awsdocdb.ModifyDBClusterInput{
		DBClusterIdentifier: aws.String("doc1"),
		EngineVersion:       aws.String("5.0.0"),
	}); err != nil {
		t.Fatalf("ModifyDBCluster: %v", err)
	}

	if _, err := client.DeleteDBCluster(ctx, &awsdocdb.DeleteDBClusterInput{
		DBClusterIdentifier: aws.String("doc1"),
		SkipFinalSnapshot:   aws.Bool(true),
	}); err != nil {
		t.Fatalf("DeleteDBCluster: %v", err)
	}
}

func TestSDKDocDBInstanceLifecycle(t *testing.T) {
	client := newDocDBSDKClient(t)
	ctx := context.Background()

	// DocumentDB instances must be created inside a cluster — the SDK enforces
	// DBClusterIdentifier as a required field for CreateDBInstance.
	if _, err := client.CreateDBCluster(ctx, &awsdocdb.CreateDBClusterInput{
		DBClusterIdentifier: aws.String("doc-host"),
		Engine:              aws.String("docdb"),
	}); err != nil {
		t.Fatalf("CreateDBCluster: %v", err)
	}

	if _, err := client.CreateDBInstance(ctx, &awsdocdb.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("docinst"),
		DBClusterIdentifier:  aws.String("doc-host"),
		Engine:               aws.String("docdb"),
		DBInstanceClass:      aws.String("db.r5.large"),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	desc, err := client.DescribeDBInstances(ctx, &awsdocdb.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String("docinst"),
	})
	if err != nil {
		t.Fatalf("DescribeDBInstances: %v", err)
	}

	if len(desc.DBInstances) != 1 {
		t.Fatalf("got %d instances, want 1", len(desc.DBInstances))
	}

	if desc.DBInstances[0].Endpoint == nil || aws.ToString(desc.DBInstances[0].Endpoint.Address) == "" {
		t.Fatal("expected endpoint to be set")
	}

	if _, err := client.ModifyDBInstance(ctx, &awsdocdb.ModifyDBInstanceInput{
		DBInstanceIdentifier: aws.String("docinst"),
		DBInstanceClass:      aws.String("db.r5.xlarge"),
	}); err != nil {
		t.Fatalf("ModifyDBInstance: %v", err)
	}

	if _, err := client.DeleteDBInstance(ctx, &awsdocdb.DeleteDBInstanceInput{
		DBInstanceIdentifier: aws.String("docinst"),
	}); err != nil {
		t.Fatalf("DeleteDBInstance: %v", err)
	}

	if _, err := client.DeleteDBCluster(ctx, &awsdocdb.DeleteDBClusterInput{
		DBClusterIdentifier: aws.String("doc-host"),
		SkipFinalSnapshot:   aws.Bool(true),
	}); err != nil {
		t.Fatalf("DeleteDBCluster: %v", err)
	}
}

func TestSDKDocDBClusterSnapshotAndRestore(t *testing.T) {
	client := newDocDBSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBCluster(ctx, &awsdocdb.CreateDBClusterInput{
		DBClusterIdentifier: aws.String("doc-src"),
		Engine:              aws.String("docdb"),
	}); err != nil {
		t.Fatalf("CreateDBCluster: %v", err)
	}

	snap, err := client.CreateDBClusterSnapshot(ctx, &awsdocdb.CreateDBClusterSnapshotInput{
		DBClusterSnapshotIdentifier: aws.String("doc-snap1"),
		DBClusterIdentifier:         aws.String("doc-src"),
	})
	if err != nil {
		t.Fatalf("CreateDBClusterSnapshot: %v", err)
	}

	if aws.ToString(snap.DBClusterSnapshot.DBClusterSnapshotIdentifier) != "doc-snap1" {
		t.Fatal("snapshot identifier mismatch")
	}

	restored, err := client.RestoreDBClusterFromSnapshot(ctx,
		&awsdocdb.RestoreDBClusterFromSnapshotInput{
			DBClusterIdentifier: aws.String("doc-restored"),
			SnapshotIdentifier:  aws.String("doc-snap1"),
			Engine:              aws.String("docdb"),
		})
	if err != nil {
		t.Fatalf("RestoreDBClusterFromSnapshot: %v", err)
	}

	if aws.ToString(restored.DBCluster.DBClusterIdentifier) != "doc-restored" {
		t.Fatal("restored cluster identifier mismatch")
	}

	if _, err := client.DeleteDBClusterSnapshot(ctx, &awsdocdb.DeleteDBClusterSnapshotInput{
		DBClusterSnapshotIdentifier: aws.String("doc-snap1"),
	}); err != nil {
		t.Fatalf("DeleteDBClusterSnapshot: %v", err)
	}
}
