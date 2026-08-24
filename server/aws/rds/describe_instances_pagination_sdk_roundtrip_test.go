package rds_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
)

// TestSDKRDSDescribeInstancesPagination asserts DescribeDBInstances honors
// MaxRecords + Marker: the first page returns the requested count with a
// Marker, and the paginator walks the rest with no overlap.
func TestSDKRDSDescribeInstancesPagination(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	ids := []string{"pg-a", "pg-b", "pg-c", "pg-d", "pg-e"}
	for _, id := range ids {
		if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
			DBInstanceIdentifier: aws.String(id),
			Engine:               aws.String("mysql"),
			DBInstanceClass:      aws.String("db.t3.micro"),
		}); err != nil {
			t.Fatalf("CreateDBInstance(%s): %v", id, err)
		}
	}

	first, err := client.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{
		MaxRecords: aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("DescribeDBInstances page 1: %v", err)
	}

	if len(first.DBInstances) != 2 {
		t.Fatalf("page 1 returned %d instances, want 2", len(first.DBInstances))
	}

	if aws.ToString(first.Marker) == "" {
		t.Fatal("page 1 Marker empty; want a continuation token")
	}

	seen := map[string]bool{}
	for _, di := range first.DBInstances {
		seen[aws.ToString(di.DBInstanceIdentifier)] = true
	}

	second, err := client.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{
		MaxRecords: aws.Int32(2),
		Marker:     first.Marker,
	})
	if err != nil {
		t.Fatalf("DescribeDBInstances page 2: %v", err)
	}

	for _, di := range second.DBInstances {
		id := aws.ToString(di.DBInstanceIdentifier)
		if seen[id] {
			t.Fatalf("instance %q appeared on both pages", id)
		}

		seen[id] = true
	}
}

// TestSDKRDSDescribeInstancesFilter asserts the engine filter narrows the
// DescribeDBInstances result to matching instances only.
func TestSDKRDSDescribeInstancesFilter(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("mysql-one"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
	}); err != nil {
		t.Fatalf("CreateDBInstance mysql: %v", err)
	}

	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("pg-one"),
		Engine:               aws.String("postgres"),
		DBInstanceClass:      aws.String("db.t3.micro"),
	}); err != nil {
		t.Fatalf("CreateDBInstance postgres: %v", err)
	}

	got, err := client.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{
		Filters: []rdstypes.Filter{{
			Name:   aws.String("engine"),
			Values: []string{"postgres"},
		}},
	})
	if err != nil {
		t.Fatalf("DescribeDBInstances filter: %v", err)
	}

	if len(got.DBInstances) != 1 ||
		aws.ToString(got.DBInstances[0].DBInstanceIdentifier) != "pg-one" {
		t.Fatalf("engine filter returned %+v, want only pg-one", got.DBInstances)
	}
}
