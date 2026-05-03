package rds_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsneptune "github.com/aws/aws-sdk-go-v2/service/neptune"

	"github.com/stackshy/cloudemu"
	awsserver "github.com/stackshy/cloudemu/server/aws"
)

// Neptune ships its own aws-sdk-go-v2 client but speaks the exact same
// query-protocol wire shape as RDS — only the Engine value ("neptune") and
// default port (8182) differ. The tests below drive the real Neptune client
// against the cloudemu RDS handler to prove the shared surface holds.

func newNeptuneSDKClient(t *testing.T) *awsneptune.Client {
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

	return awsneptune.NewFromConfig(cfg, func(o *awsneptune.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKNeptuneClusterCRUD(t *testing.T) {
	client := newNeptuneSDKClient(t)
	ctx := context.Background()

	out, err := client.CreateDBCluster(ctx, &awsneptune.CreateDBClusterInput{
		DBClusterIdentifier: aws.String("nep1"),
		Engine:              aws.String("neptune"),
	})
	if err != nil {
		t.Fatalf("CreateDBCluster: %v", err)
	}

	if aws.ToString(out.DBCluster.DBClusterIdentifier) != "nep1" {
		t.Fatalf("got cluster id %q, want nep1",
			aws.ToString(out.DBCluster.DBClusterIdentifier))
	}

	if aws.ToString(out.DBCluster.Engine) != "neptune" {
		t.Fatalf("got engine %q, want neptune", aws.ToString(out.DBCluster.Engine))
	}

	if out.DBCluster.Port == nil || *out.DBCluster.Port != 8182 {
		t.Fatalf("got port %v, want 8182", out.DBCluster.Port)
	}

	desc, err := client.DescribeDBClusters(ctx, &awsneptune.DescribeDBClustersInput{
		DBClusterIdentifier: aws.String("nep1"),
	})
	if err != nil {
		t.Fatalf("DescribeDBClusters: %v", err)
	}

	if len(desc.DBClusters) != 1 {
		t.Fatalf("got %d clusters, want 1", len(desc.DBClusters))
	}

	if _, err := client.ModifyDBCluster(ctx, &awsneptune.ModifyDBClusterInput{
		DBClusterIdentifier: aws.String("nep1"),
		EngineVersion:       aws.String("1.2.1.0"),
	}); err != nil {
		t.Fatalf("ModifyDBCluster: %v", err)
	}

	if _, err := client.DeleteDBCluster(ctx, &awsneptune.DeleteDBClusterInput{
		DBClusterIdentifier: aws.String("nep1"),
		SkipFinalSnapshot:   aws.Bool(true),
	}); err != nil {
		t.Fatalf("DeleteDBCluster: %v", err)
	}
}

func TestSDKNeptuneInstanceLifecycle(t *testing.T) {
	client := newNeptuneSDKClient(t)
	ctx := context.Background()

	// Neptune instances must be created inside a cluster — the SDK enforces
	// DBClusterIdentifier as a required field for CreateDBInstance.
	if _, err := client.CreateDBCluster(ctx, &awsneptune.CreateDBClusterInput{
		DBClusterIdentifier: aws.String("nep-host"),
		Engine:              aws.String("neptune"),
	}); err != nil {
		t.Fatalf("CreateDBCluster: %v", err)
	}

	if _, err := client.CreateDBInstance(ctx, &awsneptune.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("nepinst"),
		DBClusterIdentifier:  aws.String("nep-host"),
		Engine:               aws.String("neptune"),
		DBInstanceClass:      aws.String("db.r5.large"),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	desc, err := client.DescribeDBInstances(ctx, &awsneptune.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String("nepinst"),
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

	if _, err := client.ModifyDBInstance(ctx, &awsneptune.ModifyDBInstanceInput{
		DBInstanceIdentifier: aws.String("nepinst"),
		AllocatedStorage:     aws.Int32(50),
	}); err != nil {
		t.Fatalf("ModifyDBInstance: %v", err)
	}

	if _, err := client.DeleteDBInstance(ctx, &awsneptune.DeleteDBInstanceInput{
		DBInstanceIdentifier: aws.String("nepinst"),
		SkipFinalSnapshot:    aws.Bool(true),
	}); err != nil {
		t.Fatalf("DeleteDBInstance: %v", err)
	}

	if _, err := client.DeleteDBCluster(ctx, &awsneptune.DeleteDBClusterInput{
		DBClusterIdentifier: aws.String("nep-host"),
		SkipFinalSnapshot:   aws.Bool(true),
	}); err != nil {
		t.Fatalf("DeleteDBCluster: %v", err)
	}
}

func TestSDKNeptuneClusterSnapshotAndRestore(t *testing.T) {
	client := newNeptuneSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBCluster(ctx, &awsneptune.CreateDBClusterInput{
		DBClusterIdentifier: aws.String("nep-src"),
		Engine:              aws.String("neptune"),
	}); err != nil {
		t.Fatalf("CreateDBCluster: %v", err)
	}

	snap, err := client.CreateDBClusterSnapshot(ctx, &awsneptune.CreateDBClusterSnapshotInput{
		DBClusterSnapshotIdentifier: aws.String("nep-snap1"),
		DBClusterIdentifier:         aws.String("nep-src"),
	})
	if err != nil {
		t.Fatalf("CreateDBClusterSnapshot: %v", err)
	}

	if aws.ToString(snap.DBClusterSnapshot.DBClusterSnapshotIdentifier) != "nep-snap1" {
		t.Fatal("snapshot identifier mismatch")
	}

	restored, err := client.RestoreDBClusterFromSnapshot(ctx,
		&awsneptune.RestoreDBClusterFromSnapshotInput{
			DBClusterIdentifier: aws.String("nep-restored"),
			SnapshotIdentifier:  aws.String("nep-snap1"),
			Engine:              aws.String("neptune"),
		})
	if err != nil {
		t.Fatalf("RestoreDBClusterFromSnapshot: %v", err)
	}

	if aws.ToString(restored.DBCluster.DBClusterIdentifier) != "nep-restored" {
		t.Fatal("restored cluster identifier mismatch")
	}

	if _, err := client.DeleteDBClusterSnapshot(ctx, &awsneptune.DeleteDBClusterSnapshotInput{
		DBClusterSnapshotIdentifier: aws.String("nep-snap1"),
	}); err != nil {
		t.Fatalf("DeleteDBClusterSnapshot: %v", err)
	}
}
