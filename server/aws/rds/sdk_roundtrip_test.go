package rds_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"

	"github.com/stackshy/cloudemu"
	awsserver "github.com/stackshy/cloudemu/server/aws"
)

func newSDKClient(t *testing.T) *awsrds.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		RDS: cloud.RDS,
		// EC2 also wired so we exercise the dispatch precedence: a request
		// for RDS must claim the body before EC2 sees it.
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

	return awsrds.NewFromConfig(cfg, func(o *awsrds.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKRDSCreateDescribeInstance(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	out, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("mydb"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		MasterUsername:       aws.String("admin"),
		MasterUserPassword:   aws.String("supersecret"),
		AllocatedStorage:     aws.Int32(20),
	})
	if err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	if aws.ToString(out.DBInstance.DBInstanceIdentifier) != "mydb" {
		t.Fatalf("got id %q, want mydb", aws.ToString(out.DBInstance.DBInstanceIdentifier))
	}

	if aws.ToString(out.DBInstance.DBInstanceStatus) != "available" {
		t.Fatalf("got status %q, want available", aws.ToString(out.DBInstance.DBInstanceStatus))
	}

	got, err := client.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String("mydb"),
	})
	if err != nil {
		t.Fatalf("DescribeDBInstances: %v", err)
	}

	if len(got.DBInstances) != 1 {
		t.Fatalf("got %d instances, want 1", len(got.DBInstances))
	}

	if got.DBInstances[0].Endpoint == nil || aws.ToString(got.DBInstances[0].Endpoint.Address) == "" {
		t.Fatal("expected endpoint to be set")
	}
}

func TestSDKRDSInstanceLifecycle(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("life"),
		Engine:               aws.String("postgres"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		MasterUsername:       aws.String("admin"),
		MasterUserPassword:   aws.String("password1"),
	})
	if err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	if _, err := client.StopDBInstance(ctx, &awsrds.StopDBInstanceInput{
		DBInstanceIdentifier: aws.String("life"),
	}); err != nil {
		t.Fatalf("StopDBInstance: %v", err)
	}

	if _, err := client.StartDBInstance(ctx, &awsrds.StartDBInstanceInput{
		DBInstanceIdentifier: aws.String("life"),
	}); err != nil {
		t.Fatalf("StartDBInstance: %v", err)
	}

	if _, err := client.RebootDBInstance(ctx, &awsrds.RebootDBInstanceInput{
		DBInstanceIdentifier: aws.String("life"),
	}); err != nil {
		t.Fatalf("RebootDBInstance: %v", err)
	}

	if _, err := client.ModifyDBInstance(ctx, &awsrds.ModifyDBInstanceInput{
		DBInstanceIdentifier: aws.String("life"),
		AllocatedStorage:     aws.Int32(100),
	}); err != nil {
		t.Fatalf("ModifyDBInstance: %v", err)
	}

	got, err := client.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String("life"),
	})
	if err != nil {
		t.Fatalf("DescribeDBInstances: %v", err)
	}

	if got.DBInstances[0].AllocatedStorage == nil || *got.DBInstances[0].AllocatedStorage != 100 {
		t.Fatalf("modify did not apply, got storage=%v", got.DBInstances[0].AllocatedStorage)
	}

	if _, err := client.DeleteDBInstance(ctx, &awsrds.DeleteDBInstanceInput{
		DBInstanceIdentifier: aws.String("life"),
		SkipFinalSnapshot:    aws.Bool(true),
	}); err != nil {
		t.Fatalf("DeleteDBInstance: %v", err)
	}
}

func TestSDKRDSSnapshotAndRestore(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("snapsrc"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	snap, err := client.CreateDBSnapshot(ctx, &awsrds.CreateDBSnapshotInput{
		DBSnapshotIdentifier: aws.String("snap1"),
		DBInstanceIdentifier: aws.String("snapsrc"),
	})
	if err != nil {
		t.Fatalf("CreateDBSnapshot: %v", err)
	}

	if aws.ToString(snap.DBSnapshot.DBSnapshotIdentifier) != "snap1" {
		t.Fatalf("got snap id %q, want snap1", aws.ToString(snap.DBSnapshot.DBSnapshotIdentifier))
	}

	list, err := client.DescribeDBSnapshots(ctx, &awsrds.DescribeDBSnapshotsInput{
		DBSnapshotIdentifier: aws.String("snap1"),
	})
	if err != nil {
		t.Fatalf("DescribeDBSnapshots: %v", err)
	}

	if len(list.DBSnapshots) != 1 {
		t.Fatalf("got %d snapshots, want 1", len(list.DBSnapshots))
	}

	restored, err := client.RestoreDBInstanceFromDBSnapshot(ctx,
		&awsrds.RestoreDBInstanceFromDBSnapshotInput{
			DBInstanceIdentifier: aws.String("restored1"),
			DBSnapshotIdentifier: aws.String("snap1"),
			DBInstanceClass:      aws.String("db.t3.micro"),
		})
	if err != nil {
		t.Fatalf("RestoreDBInstanceFromDBSnapshot: %v", err)
	}

	if aws.ToString(restored.DBInstance.DBInstanceIdentifier) != "restored1" {
		t.Fatalf("got restore id %q, want restored1",
			aws.ToString(restored.DBInstance.DBInstanceIdentifier))
	}

	if _, err := client.DeleteDBSnapshot(ctx, &awsrds.DeleteDBSnapshotInput{
		DBSnapshotIdentifier: aws.String("snap1"),
	}); err != nil {
		t.Fatalf("DeleteDBSnapshot: %v", err)
	}
}

func TestSDKRDSAuroraCluster(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	out, err := client.CreateDBCluster(ctx, &awsrds.CreateDBClusterInput{
		DBClusterIdentifier: aws.String("aurora1"),
		Engine:              aws.String("aurora-postgresql"),
		MasterUsername:      aws.String("admin"),
		MasterUserPassword:  aws.String("supersecret"),
		DatabaseName:        aws.String("appdb"),
	})
	if err != nil {
		t.Fatalf("CreateDBCluster: %v", err)
	}

	if aws.ToString(out.DBCluster.DBClusterIdentifier) != "aurora1" {
		t.Fatalf("got cluster id %q, want aurora1",
			aws.ToString(out.DBCluster.DBClusterIdentifier))
	}

	if _, err := client.StopDBCluster(ctx, &awsrds.StopDBClusterInput{
		DBClusterIdentifier: aws.String("aurora1"),
	}); err != nil {
		t.Fatalf("StopDBCluster: %v", err)
	}

	if _, err := client.StartDBCluster(ctx, &awsrds.StartDBClusterInput{
		DBClusterIdentifier: aws.String("aurora1"),
	}); err != nil {
		t.Fatalf("StartDBCluster: %v", err)
	}

	snap, err := client.CreateDBClusterSnapshot(ctx, &awsrds.CreateDBClusterSnapshotInput{
		DBClusterSnapshotIdentifier: aws.String("aurora-snap1"),
		DBClusterIdentifier:         aws.String("aurora1"),
	})
	if err != nil {
		t.Fatalf("CreateDBClusterSnapshot: %v", err)
	}

	if aws.ToString(snap.DBClusterSnapshot.DBClusterSnapshotIdentifier) != "aurora-snap1" {
		t.Fatal("cluster snapshot identifier mismatch")
	}

	restored, err := client.RestoreDBClusterFromSnapshot(ctx,
		&awsrds.RestoreDBClusterFromSnapshotInput{
			DBClusterIdentifier: aws.String("aurora-restored"),
			SnapshotIdentifier:  aws.String("aurora-snap1"),
			Engine:              aws.String("aurora-postgresql"),
		})
	if err != nil {
		t.Fatalf("RestoreDBClusterFromSnapshot: %v", err)
	}

	if aws.ToString(restored.DBCluster.DBClusterIdentifier) != "aurora-restored" {
		t.Fatal("restored cluster identifier mismatch")
	}

	if _, err := client.DeleteDBClusterSnapshot(ctx, &awsrds.DeleteDBClusterSnapshotInput{
		DBClusterSnapshotIdentifier: aws.String("aurora-snap1"),
	}); err != nil {
		t.Fatalf("DeleteDBClusterSnapshot: %v", err)
	}

	if _, err := client.DeleteDBCluster(ctx, &awsrds.DeleteDBClusterInput{
		DBClusterIdentifier: aws.String("aurora1"),
		SkipFinalSnapshot:   aws.Bool(true),
	}); err != nil {
		t.Fatalf("DeleteDBCluster: %v", err)
	}
}

// Sanity check: when both RDS and EC2 handlers are wired, an EC2 request still
// reaches the EC2 handler — the RDS handler's Matches must reject non-RDS
// actions despite parsing the form first.
func TestSDKRDSRoutingDoesNotShadowEC2(t *testing.T) {
	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		RDS: cloud.RDS,
		EC2: cloud.EC2,
		VPC: cloud.VPC,
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

	// Use the EC2 SDK against the same combined server. If RDS were
	// shadowing, RunInstances would return InvalidAction.
	ec2Client := awsec2.NewFromConfig(cfg, func(o *awsec2.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})

	out, err := ec2Client.RunInstances(context.Background(), &awsec2.RunInstancesInput{
		ImageId:  aws.String("ami-1"),
		MinCount: aws.Int32(1),
		MaxCount: aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("EC2 RunInstances through combined server: %v", err)
	}

	if len(out.Instances) == 0 {
		t.Fatal("expected at least one EC2 instance")
	}
}
