package rds_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	smithy "github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
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

	// EngineVersion is defaulted when the caller omits it (Terraform reads it
	// back and diffs otherwise).
	if aws.ToString(got.DBInstances[0].EngineVersion) == "" {
		t.Fatal("expected a defaulted EngineVersion")
	}
}

// TestSDKRDSDescribeMissingSnapshot guards that a specific unknown snapshot id
// returns DBSnapshotNotFoundFault rather than an empty, error-free result.
func TestSDKRDSDescribeMissingSnapshot(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, err := client.DescribeDBSnapshots(ctx, &awsrds.DescribeDBSnapshotsInput{
		DBSnapshotIdentifier: aws.String("nope"),
	})

	var nf *rdstypes.DBSnapshotNotFoundFault
	if !errors.As(err, &nf) {
		t.Fatalf("describe missing snapshot: got %v, want DBSnapshotNotFoundFault", err)
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
		ApplyImmediately:     aws.Bool(true),
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

// TestSDKRDSDeleteWithFinalSnapshot guards that DeleteDBInstance honors
// FinalDBSnapshotIdentifier (a final snapshot is created and readable) and
// rejects the InvalidParameterCombination of no skip + no final id.
func TestSDKRDSDeleteWithFinalSnapshot(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("finaldb"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	// Neither SkipFinalSnapshot nor FinalDBSnapshotIdentifier is a hard error.
	_, err := client.DeleteDBInstance(ctx, &awsrds.DeleteDBInstanceInput{
		DBInstanceIdentifier: aws.String("finaldb"),
	})
	if err == nil {
		t.Fatal("delete without skip or final id: want InvalidParameterCombination, got nil")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterCombination" {
		t.Fatalf("delete without skip or final id: got %v, want InvalidParameterCombination", err)
	}

	// Providing a final snapshot id creates the snapshot before deleting.
	if _, err := client.DeleteDBInstance(ctx, &awsrds.DeleteDBInstanceInput{
		DBInstanceIdentifier:      aws.String("finaldb"),
		FinalDBSnapshotIdentifier: aws.String("finaldb-final"),
	}); err != nil {
		t.Fatalf("DeleteDBInstance with final snapshot: %v", err)
	}

	snaps, err := client.DescribeDBSnapshots(ctx, &awsrds.DescribeDBSnapshotsInput{
		DBSnapshotIdentifier: aws.String("finaldb-final"),
	})
	if err != nil {
		t.Fatalf("DescribeDBSnapshots for final: %v", err)
	}

	if len(snaps.DBSnapshots) != 1 {
		t.Fatalf("got %d final snapshots, want 1", len(snaps.DBSnapshots))
	}
}

// TestSDKRDSSnapshotPercentProgress guards that an available snapshot reports
// PercentProgress=100 (consistent with its Status), not 0.
func TestSDKRDSSnapshotPercentProgress(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("progsrc"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	out, err := client.CreateDBSnapshot(ctx, &awsrds.CreateDBSnapshotInput{
		DBSnapshotIdentifier: aws.String("prog-snap"),
		DBInstanceIdentifier: aws.String("progsrc"),
	})
	if err != nil {
		t.Fatalf("CreateDBSnapshot: %v", err)
	}

	if aws.ToString(out.DBSnapshot.Status) != "available" {
		t.Fatalf("snapshot status %q, want available", aws.ToString(out.DBSnapshot.Status))
	}

	if aws.ToInt32(out.DBSnapshot.PercentProgress) != 100 {
		t.Fatalf("snapshot PercentProgress=%d, want 100", aws.ToInt32(out.DBSnapshot.PercentProgress))
	}
}

// TestSDKRDSListTagsForGroups guards that parameter/option/subnet group ARNs
// are recognized taggable resources: ListTagsForResource round-trips their tags
// instead of answering DBInstanceNotFound.
func TestSDKRDSListTagsForGroups(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	pg, err := client.CreateDBParameterGroup(ctx, &awsrds.CreateDBParameterGroupInput{
		DBParameterGroupName:   aws.String("pg1"),
		DBParameterGroupFamily: aws.String("mysql8.0"),
		Description:            aws.String("pg"),
		Tags:                   []rdstypes.Tag{{Key: aws.String("team"), Value: aws.String("db")}},
	})
	if err != nil {
		t.Fatalf("CreateDBParameterGroup: %v", err)
	}

	pgTags, err := client.ListTagsForResource(ctx, &awsrds.ListTagsForResourceInput{
		ResourceName: pg.DBParameterGroup.DBParameterGroupArn,
	})
	if err != nil {
		t.Fatalf("ListTagsForResource(parameter group): %v", err)
	}

	if len(pgTags.TagList) != 1 || aws.ToString(pgTags.TagList[0].Key) != "team" {
		t.Fatalf("parameter-group tags = %+v, want team=db", pgTags.TagList)
	}

	og, err := client.CreateOptionGroup(ctx, &awsrds.CreateOptionGroupInput{
		OptionGroupName:        aws.String("og1"),
		EngineName:             aws.String("mysql"),
		MajorEngineVersion:     aws.String("8.0"),
		OptionGroupDescription: aws.String("og"),
	})
	if err != nil {
		t.Fatalf("CreateOptionGroup: %v", err)
	}

	// Tag an option group after creation, then read it back.
	if _, err := client.AddTagsToResource(ctx, &awsrds.AddTagsToResourceInput{
		ResourceName: og.OptionGroup.OptionGroupArn,
		Tags:         []rdstypes.Tag{{Key: aws.String("env"), Value: aws.String("test")}},
	}); err != nil {
		t.Fatalf("AddTagsToResource(option group): %v", err)
	}

	ogTags, err := client.ListTagsForResource(ctx, &awsrds.ListTagsForResourceInput{
		ResourceName: og.OptionGroup.OptionGroupArn,
	})
	if err != nil {
		t.Fatalf("ListTagsForResource(option group): %v", err)
	}

	if len(ogTags.TagList) != 1 || aws.ToString(ogTags.TagList[0].Value) != "test" {
		t.Fatalf("option-group tags = %+v, want env=test", ogTags.TagList)
	}

	// A subnet group ARN is also taggable (empty list, not a 404).
	sg, err := client.CreateDBSubnetGroup(ctx, &awsrds.CreateDBSubnetGroupInput{
		DBSubnetGroupName:        aws.String("sg1"),
		DBSubnetGroupDescription: aws.String("sg"),
		SubnetIds:                []string{"subnet-1", "subnet-2"},
	})
	if err != nil {
		t.Fatalf("CreateDBSubnetGroup: %v", err)
	}

	if _, err := client.ListTagsForResource(ctx, &awsrds.ListTagsForResourceInput{
		ResourceName: sg.DBSubnetGroup.DBSubnetGroupArn,
	}); err != nil {
		t.Fatalf("ListTagsForResource(subnet group): %v", err)
	}
}
