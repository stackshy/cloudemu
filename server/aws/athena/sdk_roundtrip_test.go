package athena_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsathena "github.com/aws/aws-sdk-go-v2/service/athena"
	athenatypes "github.com/aws/aws-sdk-go-v2/service/athena/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newAthenaClient(t *testing.T) *awsathena.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{Athena: cloud.Athena})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsathena.NewFromConfig(cfg, func(o *awsathena.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKWorkGroupRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := newAthenaClient(t)

	_, err := c.CreateWorkGroup(ctx, &awsathena.CreateWorkGroupInput{
		Name:        aws.String("analytics"),
		Description: aws.String("team wg"),
		Configuration: &athenatypes.WorkGroupConfiguration{
			ResultConfiguration: &athenatypes.ResultConfiguration{
				OutputLocation: aws.String("s3://results/prefix/"),
			},
			// Explicit false must round-trip, not be reset to the true default.
			EnforceWorkGroupConfiguration:   aws.Bool(false),
			PublishCloudWatchMetricsEnabled: aws.Bool(true),
			BytesScannedCutoffPerQuery:      aws.Int64(20_000_000),
			EngineVersion: &athenatypes.EngineVersion{
				SelectedEngineVersion: aws.String("AUTO"),
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateWorkGroup: %v", err)
	}

	got, err := c.GetWorkGroup(ctx, &awsathena.GetWorkGroupInput{WorkGroup: aws.String("analytics")})
	if err != nil {
		t.Fatalf("GetWorkGroup: %v", err)
	}

	wg := got.WorkGroup
	if aws.ToString(wg.Name) != "analytics" || wg.State != athenatypes.WorkGroupStateEnabled {
		t.Fatalf("unexpected identity: name=%q state=%q", aws.ToString(wg.Name), wg.State)
	}

	cfg := wg.Configuration
	if aws.ToBool(cfg.EnforceWorkGroupConfiguration) {
		t.Fatalf("explicit-false EnforceWorkGroupConfiguration did not round-trip: got true")
	}

	if !aws.ToBool(cfg.PublishCloudWatchMetricsEnabled) {
		t.Fatalf("PublishCloudWatchMetricsEnabled = false, want true")
	}

	if aws.ToInt64(cfg.BytesScannedCutoffPerQuery) != 20_000_000 {
		t.Fatalf("BytesScannedCutoffPerQuery = %d", aws.ToInt64(cfg.BytesScannedCutoffPerQuery))
	}

	if ev := cfg.EngineVersion; ev == nil || aws.ToString(ev.EffectiveEngineVersion) != "Athena engine version 3" {
		t.Fatalf("EffectiveEngineVersion not computed: %+v", cfg.EngineVersion)
	}

	if aws.ToString(cfg.ResultConfiguration.OutputLocation) != "s3://results/prefix/" {
		t.Fatalf("OutputLocation = %q", aws.ToString(cfg.ResultConfiguration.OutputLocation))
	}

	if wg.CreationTime == nil {
		t.Fatalf("CreationTime not set")
	}
}

func TestSDKWorkGroupUpdateDelta(t *testing.T) {
	ctx := context.Background()
	c := newAthenaClient(t)

	mustCreateWG(t, c, "wg1", aws.Int64(15_000_000))

	_, err := c.UpdateWorkGroup(ctx, &awsathena.UpdateWorkGroupInput{
		WorkGroup:   aws.String("wg1"),
		Description: aws.String("updated"),
		State:       athenatypes.WorkGroupStateDisabled,
		ConfigurationUpdates: &athenatypes.WorkGroupConfigurationUpdates{
			RemoveBytesScannedCutoffPerQuery: aws.Bool(true),
			EnforceWorkGroupConfiguration:    aws.Bool(false),
		},
	})
	if err != nil {
		t.Fatalf("UpdateWorkGroup: %v", err)
	}

	got, err := c.GetWorkGroup(ctx, &awsathena.GetWorkGroupInput{WorkGroup: aws.String("wg1")})
	if err != nil {
		t.Fatalf("GetWorkGroup: %v", err)
	}

	if got.WorkGroup.State != athenatypes.WorkGroupStateDisabled {
		t.Fatalf("state not updated: %q", got.WorkGroup.State)
	}

	if aws.ToString(got.WorkGroup.Description) != "updated" {
		t.Fatalf("description not updated: %q", aws.ToString(got.WorkGroup.Description))
	}

	if got.WorkGroup.Configuration.BytesScannedCutoffPerQuery != nil {
		t.Fatalf("cutoff not removed by delta: %d", aws.ToInt64(got.WorkGroup.Configuration.BytesScannedCutoffPerQuery))
	}

	if aws.ToBool(got.WorkGroup.Configuration.EnforceWorkGroupConfiguration) {
		t.Fatalf("enforce delta not applied")
	}
}

func TestSDKListWorkGroupsIncludesPrimary(t *testing.T) {
	ctx := context.Background()
	c := newAthenaClient(t)

	mustCreateWG(t, c, "zeta", nil)

	out, err := c.ListWorkGroups(ctx, &awsathena.ListWorkGroupsInput{})
	if err != nil {
		t.Fatalf("ListWorkGroups: %v", err)
	}

	names := make([]string, 0, len(out.WorkGroups))
	for _, s := range out.WorkGroups {
		names = append(names, aws.ToString(s.Name))
	}

	if len(names) != 2 || names[0] != "primary" || names[1] != "zeta" {
		t.Fatalf("deterministic order/seed wrong: %v", names)
	}
}

func TestSDKNamedQueryRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := newAthenaClient(t)

	const query = "SELECT   *\nFROM  events -- byte exact\n"

	created, err := c.CreateNamedQuery(ctx, &awsathena.CreateNamedQueryInput{
		Name:        aws.String("saved1"),
		Database:    aws.String("db1"),
		QueryString: aws.String(query),
	})
	if err != nil {
		t.Fatalf("CreateNamedQuery: %v", err)
	}

	got, err := c.GetNamedQuery(ctx, &awsathena.GetNamedQueryInput{NamedQueryId: created.NamedQueryId})
	if err != nil {
		t.Fatalf("GetNamedQuery: %v", err)
	}

	if aws.ToString(got.NamedQuery.QueryString) != query {
		t.Fatalf("QueryString not byte-exact: %q", aws.ToString(got.NamedQuery.QueryString))
	}

	if aws.ToString(got.NamedQuery.WorkGroup) != "primary" {
		t.Fatalf("default workgroup = %q", aws.ToString(got.NamedQuery.WorkGroup))
	}

	list, err := c.ListNamedQueries(ctx, &awsathena.ListNamedQueriesInput{})
	if err != nil {
		t.Fatalf("ListNamedQueries: %v", err)
	}

	if len(list.NamedQueryIds) != 1 || list.NamedQueryIds[0] != aws.ToString(created.NamedQueryId) {
		t.Fatalf("list mismatch: %v", list.NamedQueryIds)
	}
}

func TestSDKQueryExecutionAndDatabaseDDL(t *testing.T) {
	ctx := context.Background()
	c := newAthenaClient(t)

	mustCreateWG(t, c, "wg", nil)

	start, err := c.StartQueryExecution(ctx, &awsathena.StartQueryExecutionInput{
		QueryString: aws.String("CREATE DATABASE `sales`"),
		WorkGroup:   aws.String("wg"),
		ResultConfiguration: &athenatypes.ResultConfiguration{
			OutputLocation: aws.String("s3://out/"),
		},
	})
	if err != nil {
		t.Fatalf("StartQueryExecution: %v", err)
	}

	exec, err := c.GetQueryExecution(ctx, &awsathena.GetQueryExecutionInput{
		QueryExecutionId: start.QueryExecutionId,
	})
	if err != nil {
		t.Fatalf("GetQueryExecution: %v", err)
	}

	if exec.QueryExecution.Status.State != athenatypes.QueryExecutionStateSucceeded {
		t.Fatalf("state = %q, want SUCCEEDED (reason=%q)",
			exec.QueryExecution.Status.State, aws.ToString(exec.QueryExecution.Status.StateChangeReason))
	}

	if exec.QueryExecution.StatementType != athenatypes.StatementTypeDdl {
		t.Fatalf("StatementType = %q, want DDL", exec.QueryExecution.StatementType)
	}

	db, err := c.GetDatabase(ctx, &awsathena.GetDatabaseInput{
		CatalogName: aws.String("AwsDataCatalog"), DatabaseName: aws.String("sales"),
	})
	if err != nil {
		t.Fatalf("GetDatabase after DDL: %v", err)
	}

	if aws.ToString(db.Database.Name) != "sales" {
		t.Fatalf("database name = %q", aws.ToString(db.Database.Name))
	}
}

func TestSDKGetDatabaseNotFoundIsResourceNotFound(t *testing.T) {
	ctx := context.Background()
	c := newAthenaClient(t)

	_, err := c.GetDatabase(ctx, &awsathena.GetDatabaseInput{
		CatalogName: aws.String("AwsDataCatalog"), DatabaseName: aws.String("ghost"),
	})
	if err == nil {
		t.Fatalf("expected error for missing database")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("not an API error: %v", err)
	}

	if apiErr.ErrorCode() != "ResourceNotFoundException" {
		t.Fatalf("error code = %q, want ResourceNotFoundException", apiErr.ErrorCode())
	}
}

func TestSDKGetWorkGroupNotFoundIsInvalidRequest(t *testing.T) {
	ctx := context.Background()
	c := newAthenaClient(t)

	_, err := c.GetWorkGroup(ctx, &awsathena.GetWorkGroupInput{WorkGroup: aws.String("ghost")})
	if err == nil {
		t.Fatalf("expected error for missing workgroup")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("not an API error: %v", err)
	}

	if apiErr.ErrorCode() != "InvalidRequestException" {
		t.Fatalf("error code = %q, want InvalidRequestException", apiErr.ErrorCode())
	}
}

func TestSDKTagsAndQueryResults(t *testing.T) {
	ctx := context.Background()
	c := newAthenaClient(t)

	const arn = "arn:aws:athena:us-east-1:000000000000:workgroup/tagged"

	mustCreateWG(t, c, "tagged", nil)

	_, err := c.TagResource(ctx, &awsathena.TagResourceInput{
		ResourceARN: aws.String(arn),
		Tags:        []athenatypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	})
	if err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	tags, err := c.ListTagsForResource(ctx, &awsathena.ListTagsForResourceInput{ResourceARN: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if len(tags.Tags) != 1 || aws.ToString(tags.Tags[0].Key) != "env" {
		t.Fatalf("tags not round-tripped: %+v", tags.Tags)
	}

	start, err := c.StartQueryExecution(ctx, &awsathena.StartQueryExecutionInput{
		QueryString:         aws.String("CREATE DATABASE viaresults"),
		WorkGroup:           aws.String("tagged"),
		ResultConfiguration: &athenatypes.ResultConfiguration{OutputLocation: aws.String("s3://out/")},
	})
	if err != nil {
		t.Fatalf("StartQueryExecution: %v", err)
	}

	res, err := c.GetQueryResults(ctx, &awsathena.GetQueryResultsInput{QueryExecutionId: start.QueryExecutionId})
	if err != nil {
		t.Fatalf("GetQueryResults: %v", err)
	}

	if res.ResultSet == nil {
		t.Fatalf("expected non-nil ResultSet")
	}
}

func mustCreateWG(t *testing.T, c *awsathena.Client, name string, cutoff *int64) {
	t.Helper()

	_, err := c.CreateWorkGroup(context.Background(), &awsathena.CreateWorkGroupInput{
		Name: aws.String(name),
		Configuration: &athenatypes.WorkGroupConfiguration{
			ResultConfiguration:        &athenatypes.ResultConfiguration{OutputLocation: aws.String("s3://out/")},
			BytesScannedCutoffPerQuery: cutoff,
		},
	})
	if err != nil {
		t.Fatalf("CreateWorkGroup(%s): %v", name, err)
	}
}
