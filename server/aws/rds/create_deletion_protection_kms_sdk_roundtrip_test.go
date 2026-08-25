package rds_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
	smithy "github.com/aws/smithy-go"
)

// TestSDKRDSCreateDeletionProtectionHonored guards the create-time path: a DB
// instance created with DeletionProtection=true must read back true AND reject
// DeleteDBInstance with InvalidParameterCombination until the flag is cleared
// via ModifyDBInstance. Previously CreateDBInstance dropped the flag, leaving a
// "protected" DB fully deletable.
func TestSDKRDSCreateDeletionProtectionHonored(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("dp-create"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
		DeletionProtection:   aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	if !aws.ToBool(created.DBInstance.DeletionProtection) {
		t.Fatal("create response DeletionProtection=false, want true")
	}

	desc, err := client.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String("dp-create"),
	})
	if err != nil {
		t.Fatalf("DescribeDBInstances: %v", err)
	}

	if !aws.ToBool(desc.DBInstances[0].DeletionProtection) {
		t.Fatal("described DeletionProtection=false, want true")
	}

	// The flag must actually block deletion.
	_, err = client.DeleteDBInstance(ctx, &awsrds.DeleteDBInstanceInput{
		DBInstanceIdentifier: aws.String("dp-create"),
		SkipFinalSnapshot:    aws.Bool(true),
	})
	if err == nil {
		t.Fatal("delete protected instance: want InvalidParameterCombination, got nil")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterCombination" {
		t.Fatalf("delete protected instance: got %v, want InvalidParameterCombination", err)
	}

	if _, err := client.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String("dp-create"),
	}); err != nil {
		t.Fatalf("instance was deleted despite create-time protection: %v", err)
	}

	// Clearing protection via Modify lets the delete through.
	if _, err := client.ModifyDBInstance(ctx, &awsrds.ModifyDBInstanceInput{
		DBInstanceIdentifier: aws.String("dp-create"),
		DeletionProtection:   aws.Bool(false),
		ApplyImmediately:     aws.Bool(true),
	}); err != nil {
		t.Fatalf("ModifyDBInstance disable: %v", err)
	}

	if _, err := client.DeleteDBInstance(ctx, &awsrds.DeleteDBInstanceInput{
		DBInstanceIdentifier: aws.String("dp-create"),
		SkipFinalSnapshot:    aws.Bool(true),
	}); err != nil {
		t.Fatalf("DeleteDBInstance after disabling protection: %v", err)
	}
}

// TestSDKRDSCreateKmsKeyIdRoundTrips guards that an explicit KmsKeyId on
// CreateDBInstance (with StorageEncrypted) round-trips on Describe, and that an
// encrypted instance with no key supplied is back-filled with the account's
// default RDS key, matching real RDS.
func TestSDKRDSCreateKmsKeyIdRoundTrips(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const keyARN = "arn:aws:kms:us-east-1:123456789012:key/abcd-1234"

	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("enc-explicit"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
		StorageEncrypted:     aws.Bool(true),
		KmsKeyId:             aws.String(keyARN),
	}); err != nil {
		t.Fatalf("CreateDBInstance (explicit key): %v", err)
	}

	desc, err := client.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String("enc-explicit"),
	})
	if err != nil {
		t.Fatalf("DescribeDBInstances: %v", err)
	}

	inst := desc.DBInstances[0]
	if !aws.ToBool(inst.StorageEncrypted) {
		t.Fatal("StorageEncrypted=false, want true")
	}

	if got := aws.ToString(inst.KmsKeyId); got != keyARN {
		t.Fatalf("KmsKeyId=%q, want %q", got, keyARN)
	}

	// Encrypted, no key supplied -> default RDS key is back-filled.
	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("enc-default"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
		StorageEncrypted:     aws.Bool(true),
	}); err != nil {
		t.Fatalf("CreateDBInstance (default key): %v", err)
	}

	desc2, err := client.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String("enc-default"),
	})
	if err != nil {
		t.Fatalf("DescribeDBInstances (default key): %v", err)
	}

	if got := aws.ToString(desc2.DBInstances[0].KmsKeyId); got != "alias/aws/rds" {
		t.Fatalf("default KmsKeyId=%q, want alias/aws/rds", got)
	}
}

// TestSDKRDSDeleteWithLiveReplicaLeavesNoFinalSnapshot guards the delete
// ordering: deleting a source that still has a live read replica is rejected,
// and the FinalDBSnapshotIdentifier supplied on that call must NOT persist — a
// rejected delete leaves no phantom final snapshot behind.
func TestSDKRDSDeleteWithLiveReplicaLeavesNoFinalSnapshot(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("src-with-replica"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	if _, err := client.CreateDBInstanceReadReplica(ctx, &awsrds.CreateDBInstanceReadReplicaInput{
		DBInstanceIdentifier:       aws.String("rep-of-src"),
		SourceDBInstanceIdentifier: aws.String("src-with-replica"),
	}); err != nil {
		t.Fatalf("CreateDBInstanceReadReplica: %v", err)
	}

	_, err := client.DeleteDBInstance(ctx, &awsrds.DeleteDBInstanceInput{
		DBInstanceIdentifier:      aws.String("src-with-replica"),
		FinalDBSnapshotIdentifier: aws.String("orphan-final"),
	})
	if err == nil {
		t.Fatal("delete source with live replica: want InvalidDBInstanceState, got nil")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidDBInstanceState" {
		t.Fatalf("delete source with live replica: got %v, want InvalidDBInstanceState", err)
	}

	// No final snapshot may exist for the rejected delete.
	snaps, err := client.DescribeDBSnapshots(ctx, &awsrds.DescribeDBSnapshotsInput{
		DBInstanceIdentifier: aws.String("src-with-replica"),
	})
	if err != nil {
		t.Fatalf("DescribeDBSnapshots: %v", err)
	}

	if len(snaps.DBSnapshots) != 0 {
		t.Fatalf("got %d snapshots after rejected delete, want 0 (phantom final snapshot)", len(snaps.DBSnapshots))
	}

	// The source instance must still exist.
	if _, err := client.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String("src-with-replica"),
	}); err != nil {
		t.Fatalf("source was deleted despite live replica: %v", err)
	}
}
