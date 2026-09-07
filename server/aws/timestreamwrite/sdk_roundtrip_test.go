package timestreamwrite_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsts "github.com/aws/aws-sdk-go-v2/service/timestreamwrite"
	tstypes "github.com/aws/aws-sdk-go-v2/service/timestreamwrite/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newClient(t *testing.T) *awsts.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{TimestreamWrite: cloud.TimestreamWrite})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	// A custom endpoint makes the aws-sdk-go-v2 client skip Timestream's endpoint
	// discovery and hit the handler directly, mirroring terraform-provider-aws.
	return awsts.NewFromConfig(cfg, func(o *awsts.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKDatabaseAndTableLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	const dbName = "metrics"

	create, err := c.CreateDatabase(ctx, &awsts.CreateDatabaseInput{DatabaseName: aws.String(dbName)})
	if err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	wantArn := "arn:aws:timestream:us-east-1:123456789012:database/" + dbName
	if aws.ToString(create.Database.Arn) != wantArn {
		t.Fatalf("database arn = %q, want %q", aws.ToString(create.Database.Arn), wantArn)
	}

	if aws.ToString(create.Database.KmsKeyId) == "" {
		t.Fatalf("expected an AWS-managed KMS key ARN to be reported")
	}

	// TableCount starts at 0; the arn and kms key are byte-stable across reads.
	desc, err := c.DescribeDatabase(ctx, &awsts.DescribeDatabaseInput{DatabaseName: aws.String(dbName)})
	if err != nil {
		t.Fatalf("DescribeDatabase: %v", err)
	}

	if desc.Database.TableCount != 0 {
		t.Fatalf("table count = %d, want 0", desc.Database.TableCount)
	}

	if aws.ToString(desc.Database.Arn) != wantArn ||
		aws.ToString(desc.Database.KmsKeyId) != aws.ToString(create.Database.KmsKeyId) {
		t.Fatalf("computed database fields drifted across reads")
	}

	// A table with explicit retention properties.
	const tableName = "cpu"

	tcreate, err := c.CreateTable(ctx, &awsts.CreateTableInput{
		DatabaseName: aws.String(dbName),
		TableName:    aws.String(tableName),
		RetentionProperties: &tstypes.RetentionProperties{
			MemoryStoreRetentionPeriodInHours:  aws.Int64(24),
			MagneticStoreRetentionPeriodInDays: aws.Int64(73),
		},
	})
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	wantTableArn := wantArn + "/table/" + tableName
	if aws.ToString(tcreate.Table.Arn) != wantTableArn {
		t.Fatalf("table arn = %q, want %q", aws.ToString(tcreate.Table.Arn), wantTableArn)
	}

	if tcreate.Table.TableStatus != tstypes.TableStatusActive {
		t.Fatalf("table status = %s, want ACTIVE", tcreate.Table.TableStatus)
	}

	// The parent database's table count now reflects the table.
	desc2, err := c.DescribeDatabase(ctx, &awsts.DescribeDatabaseInput{DatabaseName: aws.String(dbName)})
	if err != nil {
		t.Fatalf("DescribeDatabase#2: %v", err)
	}

	if desc2.Database.TableCount != 1 {
		t.Fatalf("table count = %d, want 1", desc2.Database.TableCount)
	}

	// Retention round-trips verbatim across a Describe.
	tdesc, err := c.DescribeTable(ctx, &awsts.DescribeTableInput{
		DatabaseName: aws.String(dbName), TableName: aws.String(tableName),
	})
	if err != nil {
		t.Fatalf("DescribeTable: %v", err)
	}

	if aws.ToInt64(tdesc.Table.RetentionProperties.MemoryStoreRetentionPeriodInHours) != 24 ||
		aws.ToInt64(tdesc.Table.RetentionProperties.MagneticStoreRetentionPeriodInDays) != 73 {
		t.Fatalf("retention drifted: %+v", tdesc.Table.RetentionProperties)
	}

	if aws.ToString(tdesc.Table.Arn) != wantTableArn {
		t.Fatalf("table arn drifted across reads")
	}

	// Deleting a database that still has tables is a ValidationException.
	_, err = c.DeleteDatabase(ctx, &awsts.DeleteDatabaseInput{DatabaseName: aws.String(dbName)})

	var ve *tstypes.ValidationException
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationException deleting a non-empty database, got %T: %v", err, err)
	}

	// Update the table's retention; a later Describe reflects it with a stable arn.
	if _, err = c.UpdateTable(ctx, &awsts.UpdateTableInput{
		DatabaseName: aws.String(dbName), TableName: aws.String(tableName),
		RetentionProperties: &tstypes.RetentionProperties{
			MemoryStoreRetentionPeriodInHours:  aws.Int64(48),
			MagneticStoreRetentionPeriodInDays: aws.Int64(100),
		},
	}); err != nil {
		t.Fatalf("UpdateTable: %v", err)
	}

	tdescAfter, err := c.DescribeTable(ctx, &awsts.DescribeTableInput{
		DatabaseName: aws.String(dbName), TableName: aws.String(tableName),
	})
	if err != nil {
		t.Fatalf("DescribeTable after update: %v", err)
	}

	if aws.ToInt64(tdescAfter.Table.RetentionProperties.MemoryStoreRetentionPeriodInHours) != 48 {
		t.Fatalf("retention not updated")
	}

	if aws.ToString(tdescAfter.Table.Arn) != wantTableArn {
		t.Fatalf("table arn drifted after update")
	}

	// Delete the table, then the (now empty) database.
	if _, err = c.DeleteTable(ctx, &awsts.DeleteTableInput{
		DatabaseName: aws.String(dbName), TableName: aws.String(tableName),
	}); err != nil {
		t.Fatalf("DeleteTable: %v", err)
	}

	if _, err = c.DeleteDatabase(ctx, &awsts.DeleteDatabaseInput{DatabaseName: aws.String(dbName)}); err != nil {
		t.Fatalf("DeleteDatabase: %v", err)
	}

	_, err = c.DescribeDatabase(ctx, &awsts.DescribeDatabaseInput{DatabaseName: aws.String(dbName)})

	var nfe *tstypes.ResourceNotFoundException
	if !errors.As(err, &nfe) {
		t.Fatalf("expected ResourceNotFoundException after delete, got %T: %v", err, err)
	}
}

func TestSDKListAndTags(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	if _, err := c.CreateDatabase(ctx, &awsts.CreateDatabaseInput{DatabaseName: aws.String("db1")}); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	list, err := c.ListDatabases(ctx, &awsts.ListDatabasesInput{})
	if err != nil {
		t.Fatalf("ListDatabases: %v", err)
	}

	if len(list.Databases) != 1 {
		t.Fatalf("expected 1 database, got %d", len(list.Databases))
	}

	arn := aws.ToString(list.Databases[0].Arn)

	if _, err = c.TagResource(ctx, &awsts.TagResourceInput{
		ResourceARN: aws.String(arn),
		Tags:        []tstypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	tags, err := c.ListTagsForResource(ctx, &awsts.ListTagsForResourceInput{ResourceARN: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if len(tags.Tags) != 1 || aws.ToString(tags.Tags[0].Value) != "prod" {
		t.Fatalf("tags round-trip failed: %+v", tags.Tags)
	}
}

func TestSDKCreateTableMissingDatabase(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	_, err := c.CreateTable(ctx, &awsts.CreateTableInput{
		DatabaseName: aws.String("nope"), TableName: aws.String("t"),
	})

	var nfe *tstypes.ResourceNotFoundException
	if !errors.As(err, &nfe) {
		t.Fatalf("expected ResourceNotFoundException for a missing database, got %T: %v", err, err)
	}
}
