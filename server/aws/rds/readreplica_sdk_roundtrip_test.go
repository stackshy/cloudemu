package rds_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
)

func TestSDKRDSReadReplicaLifecycle(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("primary"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	replica, err := client.CreateDBInstanceReadReplica(ctx, &awsrds.CreateDBInstanceReadReplicaInput{
		DBInstanceIdentifier:       aws.String("replica-1"),
		SourceDBInstanceIdentifier: aws.String("primary"),
	})
	if err != nil {
		t.Fatalf("CreateDBInstanceReadReplica: %v", err)
	}

	if aws.ToString(replica.DBInstance.ReadReplicaSourceDBInstanceIdentifier) != "primary" {
		t.Fatalf("replica source = %q, want primary",
			aws.ToString(replica.DBInstance.ReadReplicaSourceDBInstanceIdentifier))
	}

	// The primary lists the replica.
	src, err := client.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String("primary"),
	})
	if err != nil {
		t.Fatalf("DescribeDBInstances: %v", err)
	}

	ids := src.DBInstances[0].ReadReplicaDBInstanceIdentifiers
	if len(ids) != 1 || ids[0] != "replica-1" {
		t.Fatalf("primary replica list = %v, want [replica-1]", ids)
	}

	// Promote detaches it.
	promoted, err := client.PromoteReadReplica(ctx, &awsrds.PromoteReadReplicaInput{
		DBInstanceIdentifier: aws.String("replica-1"),
	})
	if err != nil {
		t.Fatalf("PromoteReadReplica: %v", err)
	}

	if aws.ToString(promoted.DBInstance.ReadReplicaSourceDBInstanceIdentifier) != "" {
		t.Fatalf("promoted replica still has a source")
	}
}
