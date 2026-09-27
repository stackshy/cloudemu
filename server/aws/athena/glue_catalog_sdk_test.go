package athena_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsathena "github.com/aws/aws-sdk-go-v2/service/athena"
	athenatypes "github.com/aws/aws-sdk-go-v2/service/athena/types"
	awsglue "github.com/aws/aws-sdk-go-v2/service/glue"
	gluetypes "github.com/aws/aws-sdk-go-v2/service/glue/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newAthenaGlueClients serves Athena and Glue from one provider, the way
// cloudemu serve does.
func newAthenaGlueClients(t *testing.T) (*awsathena.Client, *awsglue.Client) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{Athena: cloud.Athena, Glue: cloud.Glue})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	ath := awsathena.NewFromConfig(cfg, func(o *awsathena.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	gl := awsglue.NewFromConfig(cfg, func(o *awsglue.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	return ath, gl
}

func TestSDKGlueCatalogIsAwsDataCatalog(t *testing.T) {
	ctx := context.Background()
	ath, gl := newAthenaGlueClients(t)

	if _, err := gl.CreateDatabase(ctx, &awsglue.CreateDatabaseInput{
		DatabaseInput: &gluetypes.DatabaseInput{Name: aws.String("lake")},
	}); err != nil {
		t.Fatalf("glue CreateDatabase: %v", err)
	}

	if _, err := gl.CreateTable(ctx, &awsglue.CreateTableInput{
		DatabaseName: aws.String("lake"),
		TableInput: &gluetypes.TableInput{
			Name:      aws.String("events"),
			TableType: aws.String("EXTERNAL_TABLE"),
			StorageDescriptor: &gluetypes.StorageDescriptor{
				Columns:  []gluetypes.Column{{Name: aws.String("id"), Type: aws.String("bigint")}},
				Location: aws.String("s3://lake/events/"),
			},
		},
	}); err != nil {
		t.Fatalf("glue CreateTable: %v", err)
	}

	dbs, err := ath.ListDatabases(ctx, &awsathena.ListDatabasesInput{CatalogName: aws.String("AwsDataCatalog")})
	if err != nil || len(dbs.DatabaseList) != 1 || aws.ToString(dbs.DatabaseList[0].Name) != "lake" {
		t.Fatalf("athena ListDatabases = %+v, %v", dbs, err)
	}

	list, err := ath.ListTableMetadata(ctx, &awsathena.ListTableMetadataInput{
		CatalogName: aws.String("AwsDataCatalog"), DatabaseName: aws.String("lake"),
	})
	if err != nil || len(list.TableMetadataList) != 1 || aws.ToString(list.TableMetadataList[0].Name) != "events" {
		t.Fatalf("athena ListTableMetadata = %+v, %v", list, err)
	}

	tm, err := ath.GetTableMetadata(ctx, &awsathena.GetTableMetadataInput{
		CatalogName: aws.String("AwsDataCatalog"), DatabaseName: aws.String("lake"), TableName: aws.String("events"),
	})
	if err != nil {
		t.Fatalf("GetTableMetadata: %v", err)
	}

	got := tm.TableMetadata
	if aws.ToString(got.TableType) != "EXTERNAL_TABLE" || len(got.Columns) != 1 ||
		aws.ToString(got.Columns[0].Type) != "bigint" || got.Parameters["location"] != "s3://lake/events/" || got.CreateTime == nil {
		t.Fatalf("TableMetadata = %+v", got)
	}

	_, err = ath.GetTableMetadata(ctx, &awsathena.GetTableMetadataInput{
		CatalogName: aws.String("AwsDataCatalog"), DatabaseName: aws.String("lake"), TableName: aws.String("ghost"),
	})

	var metaErr *athenatypes.MetadataException
	if !errors.As(err, &metaErr) {
		t.Fatalf("missing table error = %v, want MetadataException", err)
	}

	// Athena DDL lands in Glue.
	start, err := ath.StartQueryExecution(ctx, &awsathena.StartQueryExecutionInput{
		QueryString:         aws.String("CREATE DATABASE reports COMMENT 'made by athena'"),
		ResultConfiguration: &athenatypes.ResultConfiguration{OutputLocation: aws.String("s3://out/")},
	})
	if err != nil {
		t.Fatalf("StartQueryExecution: %v", err)
	}

	gdb, err := gl.GetDatabase(ctx, &awsglue.GetDatabaseInput{Name: aws.String("reports")})
	if err != nil || aws.ToString(gdb.Database.Description) != "made by athena" {
		t.Fatalf("glue GetDatabase after Athena DDL = %+v, %v", gdb, err)
	}

	qe, err := ath.GetQueryExecution(ctx, &awsathena.GetQueryExecutionInput{QueryExecutionId: start.QueryExecutionId})
	if err != nil {
		t.Fatalf("GetQueryExecution: %v", err)
	}

	if want := "s3://out/" + aws.ToString(start.QueryExecutionId) + ".txt"; aws.ToString(qe.QueryExecution.ResultConfiguration.OutputLocation) != want {
		t.Fatalf("DDL OutputLocation = %q, want %q", aws.ToString(qe.QueryExecution.ResultConfiguration.OutputLocation), want)
	}
}

func TestSDKEnforcedWorkGroupOutputLocationWins(t *testing.T) {
	ctx := context.Background()
	ath, _ := newAthenaGlueClients(t)

	for _, enforce := range []bool{true, false} {
		name := "wg-enforce-false"
		want := "s3://client/"

		if enforce {
			name = "wg-enforce-true"
			want = "s3://wg-results/"
		}

		if _, err := ath.CreateWorkGroup(ctx, &awsathena.CreateWorkGroupInput{
			Name: aws.String(name),
			Configuration: &athenatypes.WorkGroupConfiguration{
				EnforceWorkGroupConfiguration: aws.Bool(enforce),
				ResultConfiguration:           &athenatypes.ResultConfiguration{OutputLocation: aws.String("s3://wg-results/")},
			},
		}); err != nil {
			t.Fatalf("CreateWorkGroup %s: %v", name, err)
		}

		start, err := ath.StartQueryExecution(ctx, &awsathena.StartQueryExecutionInput{
			QueryString:         aws.String("SELECT 1"),
			WorkGroup:           aws.String(name),
			ResultConfiguration: &athenatypes.ResultConfiguration{OutputLocation: aws.String("s3://client/")},
		})
		if err != nil {
			t.Fatalf("StartQueryExecution %s: %v", name, err)
		}

		qe, err := ath.GetQueryExecution(ctx, &awsathena.GetQueryExecutionInput{QueryExecutionId: start.QueryExecutionId})
		if err != nil {
			t.Fatalf("GetQueryExecution: %v", err)
		}

		got := aws.ToString(qe.QueryExecution.ResultConfiguration.OutputLocation)
		if !strings.HasPrefix(got, want) || !strings.HasSuffix(got, aws.ToString(start.QueryExecutionId)+".csv") {
			t.Fatalf("%s OutputLocation = %q, want %s<id>.csv", name, got, want)
		}
	}
}
