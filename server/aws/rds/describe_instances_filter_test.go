package rds_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	smithy "github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newRDSClient(t *testing.T) *awsrds.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{RDS: cloud.RDS}))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")))
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsrds.NewFromConfig(cfg, func(o *awsrds.Options) { o.BaseEndpoint = aws.String(ts.URL) })
}

// TestDescribeDBInstancesUnknownFilterErrors pins that an unrecognized
// DescribeDBInstances filter name is rejected with InvalidParameterValue (real
// RDS), not silently returning an empty result set.
func TestDescribeDBInstancesUnknownFilterErrors(t *testing.T) {
	ctx := context.Background()
	c := newRDSClient(t)

	if _, err := c.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("db1"), Engine: aws.String("mysql"),
		DBInstanceClass: aws.String("db.t3.micro"), MasterUsername: aws.String("admin"),
		MasterUserPassword: aws.String("password123"), AllocatedStorage: aws.Int32(20),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	_, err := c.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{
		Filters: []rdstypes.Filter{{Name: aws.String("not-a-real-filter"), Values: []string{"x"}}},
	})
	if err == nil {
		t.Fatal("DescribeDBInstances with an unknown filter succeeded, want an error")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not an API error: %v", err)
	}

	if apiErr.ErrorCode() != "InvalidParameterValue" {
		t.Fatalf("error code = %q, want InvalidParameterValue", apiErr.ErrorCode())
	}
}

// TestDescribeDBInstancesKnownFilterApplies pins that a modeled filter (engine)
// still narrows the result set after the unknown-filter validation was added.
func TestDescribeDBInstancesKnownFilterApplies(t *testing.T) {
	ctx := context.Background()
	c := newRDSClient(t)

	create := func(id, engine string) {
		if _, err := c.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
			DBInstanceIdentifier: aws.String(id), Engine: aws.String(engine),
			DBInstanceClass: aws.String("db.t3.micro"), MasterUsername: aws.String("admin"),
			MasterUserPassword: aws.String("password123"), AllocatedStorage: aws.Int32(20),
		}); err != nil {
			t.Fatalf("CreateDBInstance(%s): %v", id, err)
		}
	}

	create("mysql-db", "mysql")
	create("pg-db", "postgres")

	out, err := c.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{
		Filters: []rdstypes.Filter{{Name: aws.String("engine"), Values: []string{"postgres"}}},
	})
	if err != nil {
		t.Fatalf("DescribeDBInstances: %v", err)
	}

	if len(out.DBInstances) != 1 || aws.ToString(out.DBInstances[0].DBInstanceIdentifier) != "pg-db" {
		t.Fatalf("engine=postgres returned %d instances, want only pg-db", len(out.DBInstances))
	}
}
