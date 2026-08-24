package rds_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"

	"github.com/stackshy/cloudemu/v2"
	cloudconfig "github.com/stackshy/cloudemu/v2/config"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestAsyncSettleWireRDS pins that a real SDK client sees a DB instance as
// creating->available and a reboot as available->rebooting->available through
// the wire when AsyncSettle is on, driven by the FakeClock.
func TestAsyncSettleWireRDS(t *testing.T) {
	fc := cloudconfig.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	cloud := cloudemu.NewAWS(cloudconfig.WithClock(fc), cloudconfig.WithAsyncSettle())
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{RDS: cloud.RDS}))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")))
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	c := awsrds.NewFromConfig(cfg, func(o *awsrds.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ctx := context.Background()

	if _, err := c.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("db1"), Engine: aws.String("mysql"),
		DBInstanceClass: aws.String("db.t3.micro"), MasterUsername: aws.String("admin"),
		MasterUserPassword: aws.String("password123"), AllocatedStorage: aws.Int32(20),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	desc, err := c.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{DBInstanceIdentifier: aws.String("db1")})
	if err != nil {
		t.Fatalf("DescribeDBInstances: %v", err)
	}
	if got := aws.ToString(desc.DBInstances[0].DBInstanceStatus); got != "creating" {
		t.Fatalf("status before settle = %q, want creating", got)
	}

	fc.Advance(4 * time.Second) // past DefaultDBInstanceSettle (3s)
	desc, _ = c.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{DBInstanceIdentifier: aws.String("db1")})
	if got := aws.ToString(desc.DBInstances[0].DBInstanceStatus); got != "available" {
		t.Fatalf("status after settle = %q, want available", got)
	}

	if _, err := c.RebootDBInstance(ctx, &awsrds.RebootDBInstanceInput{DBInstanceIdentifier: aws.String("db1")}); err != nil {
		t.Fatalf("RebootDBInstance: %v", err)
	}
	desc, _ = c.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{DBInstanceIdentifier: aws.String("db1")})
	if got := aws.ToString(desc.DBInstances[0].DBInstanceStatus); got != "rebooting" {
		t.Fatalf("status during reboot = %q, want rebooting", got)
	}

	fc.Advance(2 * time.Second) // past DefaultDBRebootSettle (1s)
	desc, _ = c.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{DBInstanceIdentifier: aws.String("db1")})
	if got := aws.ToString(desc.DBInstances[0].DBInstanceStatus); got != "available" {
		t.Fatalf("status after reboot = %q, want available", got)
	}
}
