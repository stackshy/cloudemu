package rds_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
)

// TestSDKRDSReadReplicaInheritsSourceAttributes guards that a read replica
// inherits DBName and DBParameterGroupName from its source instance when the
// caller doesn't override them, matching real RDS: "All other attributes
// ... are inherited from the source DB instance ... except as specified."
func TestSDKRDSReadReplicaInheritsSourceAttributes(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("inherit-src"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
		DBName:               aws.String("appdb"),
		DBParameterGroupName: aws.String("custom-mysql-params"),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	replica, err := client.CreateDBInstanceReadReplica(ctx, &awsrds.CreateDBInstanceReadReplicaInput{
		DBInstanceIdentifier:       aws.String("inherit-replica"),
		SourceDBInstanceIdentifier: aws.String("inherit-src"),
	})
	if err != nil {
		t.Fatalf("CreateDBInstanceReadReplica: %v", err)
	}

	if got := aws.ToString(replica.DBInstance.DBName); got != "appdb" {
		t.Fatalf("replica DBName = %q, want appdb (inherited from source)", got)
	}

	if len(replica.DBInstance.DBParameterGroups) != 1 ||
		aws.ToString(replica.DBInstance.DBParameterGroups[0].DBParameterGroupName) != "custom-mysql-params" {
		t.Fatalf("replica DBParameterGroups = %v, want [custom-mysql-params] (inherited from source)",
			replica.DBInstance.DBParameterGroups)
	}
}

// TestSDKRDSRestoreFromSnapshotInheritsSourceAttributes guards that
// RestoreDBInstanceFromDBSnapshot carries DBName and the source instance's
// non-default Port onto the restored instance, matching the API docs:
// Port "Default: The same port as the original DB instance".
func TestSDKRDSRestoreFromSnapshotInheritsSourceAttributes(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("restore-src"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
		DBName:               aws.String("appdb"),
		Port:                 aws.Int32(3307), // non-default MySQL port
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	if _, err := client.CreateDBSnapshot(ctx, &awsrds.CreateDBSnapshotInput{
		DBSnapshotIdentifier: aws.String("restore-snap"),
		DBInstanceIdentifier: aws.String("restore-src"),
	}); err != nil {
		t.Fatalf("CreateDBSnapshot: %v", err)
	}

	restored, err := client.RestoreDBInstanceFromDBSnapshot(ctx,
		&awsrds.RestoreDBInstanceFromDBSnapshotInput{
			DBInstanceIdentifier: aws.String("restored-inherit"),
			DBSnapshotIdentifier: aws.String("restore-snap"),
		})
	if err != nil {
		t.Fatalf("RestoreDBInstanceFromDBSnapshot: %v", err)
	}

	if got := aws.ToString(restored.DBInstance.DBName); got != "appdb" {
		t.Fatalf("restored DBName = %q, want appdb (inherited from source)", got)
	}

	if restored.DBInstance.Endpoint == nil || aws.ToInt32(restored.DBInstance.Endpoint.Port) != 3307 {
		t.Fatalf("restored Port = %v, want 3307 (inherited from source, not the engine default)",
			restored.DBInstance.Endpoint)
	}
}
