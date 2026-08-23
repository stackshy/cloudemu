package glue_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsglue "github.com/aws/aws-sdk-go-v2/service/glue"
	gluetypes "github.com/aws/aws-sdk-go-v2/service/glue/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newGlueClient(t *testing.T) *awsglue.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{Glue: cloud.Glue})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsglue.NewFromConfig(cfg, func(o *awsglue.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKDatabaseTablePartitionCRUD(t *testing.T) {
	ctx := context.Background()
	c := newGlueClient(t)

	_, err := c.CreateDatabase(ctx, &awsglue.CreateDatabaseInput{
		DatabaseInput: &gluetypes.DatabaseInput{Name: aws.String("analytics"), Description: aws.String("d")},
	})
	if err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	getDB, err := c.GetDatabase(ctx, &awsglue.GetDatabaseInput{Name: aws.String("analytics")})
	if err != nil {
		t.Fatalf("GetDatabase: %v", err)
	}

	if aws.ToString(getDB.Database.Name) != "analytics" {
		t.Fatalf("db name = %q", aws.ToString(getDB.Database.Name))
	}

	_, err = c.CreateTable(ctx, &awsglue.CreateTableInput{
		DatabaseName: aws.String("analytics"),
		TableInput: &gluetypes.TableInput{
			Name: aws.String("events"),
			StorageDescriptor: &gluetypes.StorageDescriptor{
				Columns: []gluetypes.Column{{Name: aws.String("id"), Type: aws.String("string")}},
			},
			PartitionKeys: []gluetypes.Column{{Name: aws.String("dt"), Type: aws.String("string")}},
		},
	})
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	getTbl, err := c.GetTable(ctx, &awsglue.GetTableInput{
		DatabaseName: aws.String("analytics"), Name: aws.String("events"),
	})
	if err != nil {
		t.Fatalf("GetTable: %v", err)
	}

	if len(getTbl.Table.StorageDescriptor.Columns) != 1 {
		t.Fatalf("columns not round-tripped: %+v", getTbl.Table.StorageDescriptor)
	}

	_, err = c.CreatePartition(ctx, &awsglue.CreatePartitionInput{
		DatabaseName: aws.String("analytics"), TableName: aws.String("events"),
		PartitionInput: &gluetypes.PartitionInput{Values: []string{"2024-01-01"}},
	})
	if err != nil {
		t.Fatalf("CreatePartition: %v", err)
	}

	getPart, err := c.GetPartition(ctx, &awsglue.GetPartitionInput{
		DatabaseName: aws.String("analytics"), TableName: aws.String("events"),
		PartitionValues: []string{"2024-01-01"},
	})
	if err != nil {
		t.Fatalf("GetPartition: %v", err)
	}

	if getPart.Partition.Values[0] != "2024-01-01" {
		t.Fatalf("partition values = %v", getPart.Partition.Values)
	}
}

func TestSDKDuplicateDatabaseTypedError(t *testing.T) {
	ctx := context.Background()
	c := newGlueClient(t)

	in := &awsglue.CreateDatabaseInput{DatabaseInput: &gluetypes.DatabaseInput{Name: aws.String("dup")}}
	if _, err := c.CreateDatabase(ctx, in); err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err := c.CreateDatabase(ctx, in)

	var already *gluetypes.AlreadyExistsException
	if !errors.As(err, &already) {
		t.Fatalf("duplicate create err = %v, want *AlreadyExistsException", err)
	}
}

func TestSDKGetMissingTableTypedError(t *testing.T) {
	ctx := context.Background()
	c := newGlueClient(t)

	if _, err := c.CreateDatabase(ctx, &awsglue.CreateDatabaseInput{
		DatabaseInput: &gluetypes.DatabaseInput{Name: aws.String("db")},
	}); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	_, err := c.GetTable(ctx, &awsglue.GetTableInput{
		DatabaseName: aws.String("db"), Name: aws.String("nope"),
	})

	var notFound *gluetypes.EntityNotFoundException
	if !errors.As(err, &notFound) {
		t.Fatalf("get missing table err = %v, want *EntityNotFoundException", err)
	}
}

func TestSDKJobRunLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newGlueClient(t)

	_, err := c.CreateJob(ctx, &awsglue.CreateJobInput{
		Name: aws.String("etl"), Role: aws.String("arn:aws:iam::123456789012:role/r"),
		Command: &gluetypes.JobCommand{Name: aws.String("glueetl")},
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	run, err := c.StartJobRun(ctx, &awsglue.StartJobRunInput{JobName: aws.String("etl")})
	if err != nil {
		t.Fatalf("StartJobRun: %v", err)
	}

	got, err := c.GetJobRun(ctx, &awsglue.GetJobRunInput{
		JobName: aws.String("etl"), RunId: run.JobRunId,
	})
	if err != nil {
		t.Fatalf("GetJobRun: %v", err)
	}

	if got.JobRun.JobRunState != gluetypes.JobRunStateSucceeded {
		t.Fatalf("run state = %s, want SUCCEEDED", got.JobRun.JobRunState)
	}
}

// TestSDKGetJobAppliesDefaults verifies GetJob reports GlueVersion and Timeout
// defaults when the job was created without them, matching real AWS Glue.
func TestSDKGetJobAppliesDefaults(t *testing.T) {
	ctx := context.Background()
	c := newGlueClient(t)

	if _, err := c.CreateJob(ctx, &awsglue.CreateJobInput{
		Name: aws.String("defaults-job"), Role: aws.String("arn:aws:iam::123456789012:role/r"),
		Command: &gluetypes.JobCommand{Name: aws.String("glueetl")},
	}); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	got, err := c.GetJob(ctx, &awsglue.GetJobInput{JobName: aws.String("defaults-job")})
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}

	if aws.ToString(got.Job.GlueVersion) == "" {
		t.Fatal("GlueVersion is empty, want a default")
	}

	if aws.ToInt32(got.Job.Timeout) != 2880 {
		t.Fatalf("Timeout = %d, want default 2880", aws.ToInt32(got.Job.Timeout))
	}
}

// TestSDKGetJobHonoursExplicitValues verifies caller-supplied GlueVersion and
// Timeout are not overwritten by defaults.
func TestSDKGetJobHonoursExplicitValues(t *testing.T) {
	ctx := context.Background()
	c := newGlueClient(t)

	if _, err := c.CreateJob(ctx, &awsglue.CreateJobInput{
		Name: aws.String("explicit-job"), Role: aws.String("arn:aws:iam::123456789012:role/r"),
		Command:     &gluetypes.JobCommand{Name: aws.String("glueetl")},
		GlueVersion: aws.String("4.0"), Timeout: aws.Int32(60),
	}); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	got, err := c.GetJob(ctx, &awsglue.GetJobInput{JobName: aws.String("explicit-job")})
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}

	if aws.ToString(got.Job.GlueVersion) != "4.0" || aws.ToInt32(got.Job.Timeout) != 60 {
		t.Fatalf("GlueVersion=%q Timeout=%d, want 4.0/60",
			aws.ToString(got.Job.GlueVersion), aws.ToInt32(got.Job.Timeout))
	}
}

func TestSDKTags(t *testing.T) {
	ctx := context.Background()
	c := newGlueClient(t)

	arn := "arn:aws:glue:us-east-1:123456789012:database/db"

	if _, err := c.TagResource(ctx, &awsglue.TagResourceInput{
		ResourceArn: aws.String(arn), TagsToAdd: map[string]string{"env": "prod"},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	got, err := c.GetTags(ctx, &awsglue.GetTagsInput{ResourceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("GetTags: %v", err)
	}

	if got.Tags["env"] != "prod" {
		t.Fatalf("tags = %v", got.Tags)
	}
}

func TestSDKCrawlerLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newGlueClient(t)

	_, err := c.CreateCrawler(ctx, &awsglue.CreateCrawlerInput{
		Name: aws.String("c"), Role: aws.String("r"), DatabaseName: aws.String("db"),
		Targets: &gluetypes.CrawlerTargets{
			S3Targets: []gluetypes.S3Target{{Path: aws.String("s3://bucket/prefix")}},
		},
	})
	if err != nil {
		t.Fatalf("CreateCrawler: %v", err)
	}

	if _, err := c.StartCrawler(ctx, &awsglue.StartCrawlerInput{Name: aws.String("c")}); err != nil {
		t.Fatalf("StartCrawler: %v", err)
	}

	got, err := c.GetCrawler(ctx, &awsglue.GetCrawlerInput{Name: aws.String("c")})
	if err != nil {
		t.Fatalf("GetCrawler: %v", err)
	}

	if aws.ToString(got.Crawler.Name) != "c" {
		t.Fatalf("crawler name = %q", aws.ToString(got.Crawler.Name))
	}
}

func TestSDKSynthesizedOpReturnsEmpty(t *testing.T) {
	ctx := context.Background()
	c := newGlueClient(t)

	// A synthesized read-only op should succeed with an empty page rather than
	// erroring — the wire contract is preserved.
	out, err := c.ListMLTransforms(ctx, &awsglue.ListMLTransformsInput{})
	if err != nil {
		t.Fatalf("ListMLTransforms: %v", err)
	}

	if len(out.TransformIds) != 0 {
		t.Fatalf("expected no transforms, got %d", len(out.TransformIds))
	}
}

func TestSDKDeleteSchemaVersionsReportsFailingVersion(t *testing.T) {
	ctx := context.Background()
	c := newGlueClient(t)

	if _, err := c.CreateSchema(ctx, &awsglue.CreateSchemaInput{
		SchemaName:       aws.String("s"),
		DataFormat:       gluetypes.DataFormatAvro,
		Compatibility:    gluetypes.CompatibilityNone,
		SchemaDefinition: aws.String(`{"type":"record","name":"r","fields":[]}`),
	}); err != nil {
		t.Fatalf("CreateSchema: %v", err)
	}

	// Version 5 does not exist; the error item must name that version number,
	// not the zero value.
	out, err := c.DeleteSchemaVersions(ctx, &awsglue.DeleteSchemaVersionsInput{
		SchemaId: &gluetypes.SchemaId{SchemaName: aws.String("s")},
		Versions: aws.String("5"),
	})
	if err != nil {
		t.Fatalf("DeleteSchemaVersions: %v", err)
	}

	if len(out.SchemaVersionErrors) != 1 || aws.ToInt64(out.SchemaVersionErrors[0].VersionNumber) != 5 {
		t.Fatalf("SchemaVersionErrors = %+v, want one entry with VersionNumber==5", out.SchemaVersionErrors)
	}
}

// smithyAPIError is asserted to ensure our error type is a modeled smithy error.
var _ smithy.APIError = (*gluetypes.EntityNotFoundException)(nil)
