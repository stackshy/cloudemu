// dynamodb_settle_test.go — real-user tests for asynchronous DynamoDB
// table/GSI lifecycle status (CREATING/UPDATING -> ACTIVE). They drive the
// genuine aws-sdk-go-v2 client against the emulator with async settling enabled
// and a FakeClock, so an immediate DescribeTable observes the transient status
// and advancing the clock resolves it to ACTIVE. A default-off case guards the
// unchanged synchronous behavior.
package dynamodb_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stackshy/cloudemu/v2"
	emuconfig "github.com/stackshy/cloudemu/v2/config"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// settleWindow mirrors the provider's unexported table/GSI settle durations
// (all 2s); advancing the FakeClock by it resolves a transient status to ACTIVE.
const settleWindow = 2 * time.Second

func newSettleDDBEnv(t *testing.T) (*dynamodb.Client, *emuconfig.FakeClock) {
	t.Helper()

	fc := emuconfig.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	provider := cloudemu.NewAWS(emuconfig.WithClock(fc), emuconfig.WithAsyncSettle())
	srv := awsserver.New(awsserver.Drivers{DynamoDB: provider.DynamoDB})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	require.NoError(t, err)

	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
		o.HTTPClient = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	})

	return client, fc
}

func describeStatus(t *testing.T, client *dynamodb.Client, table string) ddbtypes.TableStatus {
	t.Helper()

	out, err := client.DescribeTable(context.Background(), &dynamodb.DescribeTableInput{TableName: aws.String(table)})
	require.NoError(t, err)

	return out.Table.TableStatus
}

// TestSettleTableCreateStatus: a real CreateTable reports CREATING, an immediate
// DescribeTable still sees CREATING, and once the clock advances past the window
// it reads ACTIVE.
func TestSettleTableCreateStatus(t *testing.T) {
	client, fc := newSettleDDBEnv(t)
	ctx := context.Background()

	out, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:            aws.String("orders"),
		KeySchema:            []ddbtypes.KeySchemaElement{{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash}},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS}},
		BillingMode:          ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)
	assert.Equal(t, ddbtypes.TableStatusCreating, out.TableDescription.TableStatus)

	assert.Equal(t, ddbtypes.TableStatusCreating, describeStatus(t, client, "orders"))

	fc.Advance(settleWindow)
	assert.Equal(t, ddbtypes.TableStatusActive, describeStatus(t, client, "orders"))
}

// TestSettleTableUpdateStatus: an UpdateTable throughput change drives the table
// through UPDATING back to ACTIVE.
func TestSettleTableUpdateStatus(t *testing.T) {
	client, fc := newSettleDDBEnv(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:            aws.String("orders"),
		KeySchema:            []ddbtypes.KeySchemaElement{{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash}},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS}},
		BillingMode:          ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)
	fc.Advance(settleWindow)
	require.Equal(t, ddbtypes.TableStatusActive, describeStatus(t, client, "orders"))

	upd, err := client.UpdateTable(ctx, &dynamodb.UpdateTableInput{
		TableName:   aws.String("orders"),
		BillingMode: ddbtypes.BillingModeProvisioned,
		ProvisionedThroughput: &ddbtypes.ProvisionedThroughput{
			ReadCapacityUnits:  aws.Int64(10),
			WriteCapacityUnits: aws.Int64(10),
		},
	})
	require.NoError(t, err)
	assert.Equal(t, ddbtypes.TableStatusUpdating, upd.TableDescription.TableStatus)
	assert.Equal(t, ddbtypes.TableStatusUpdating, describeStatus(t, client, "orders"))

	fc.Advance(settleWindow)
	assert.Equal(t, ddbtypes.TableStatusActive, describeStatus(t, client, "orders"))
}

// TestSettleGSICreateStatus: a GSI added via UpdateTable reports IndexStatus
// CREATING until the window elapses, then ACTIVE, while the table stays ACTIVE.
func TestSettleGSICreateStatus(t *testing.T) {
	client, fc := newSettleDDBEnv(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String("orders"),
		KeySchema: []ddbtypes.KeySchemaElement{{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash}},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)
	fc.Advance(settleWindow)

	_, err = client.UpdateTable(ctx, &dynamodb.UpdateTableInput{
		TableName: aws.String("orders"),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("gk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		GlobalSecondaryIndexUpdates: []ddbtypes.GlobalSecondaryIndexUpdate{{
			Create: &ddbtypes.CreateGlobalSecondaryIndexAction{
				IndexName:  aws.String("by-gk"),
				KeySchema:  []ddbtypes.KeySchemaElement{{AttributeName: aws.String("gk"), KeyType: ddbtypes.KeyTypeHash}},
				Projection: &ddbtypes.Projection{ProjectionType: ddbtypes.ProjectionTypeAll},
			},
		}},
	})
	require.NoError(t, err)

	out, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String("orders")})
	require.NoError(t, err)
	require.Len(t, out.Table.GlobalSecondaryIndexes, 1)
	assert.Equal(t, ddbtypes.IndexStatusCreating, out.Table.GlobalSecondaryIndexes[0].IndexStatus)
	assert.Equal(t, ddbtypes.TableStatusActive, out.Table.TableStatus)

	fc.Advance(settleWindow)
	out, err = client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String("orders")})
	require.NoError(t, err)
	assert.Equal(t, ddbtypes.IndexStatusActive, out.Table.GlobalSecondaryIndexes[0].IndexStatus)
}

// TestSettleDefaultOff guards the blast radius: with the default options
// (async settling off) a real CreateTable reports ACTIVE immediately, exactly as
// before this change.
func TestSettleDefaultOff(t *testing.T) {
	provider := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{DynamoDB: provider.DynamoDB})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	require.NoError(t, err)

	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
		o.HTTPClient = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	})
	ctx := context.Background()

	out, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:            aws.String("orders"),
		KeySchema:            []ddbtypes.KeySchemaElement{{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash}},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS}},
		BillingMode:          ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)
	assert.Equal(t, ddbtypes.TableStatusActive, out.TableDescription.TableStatus)
	assert.Equal(t, ddbtypes.TableStatusActive, describeStatus(t, client, "orders"))
}
