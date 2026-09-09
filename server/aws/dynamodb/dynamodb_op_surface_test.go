// dynamodb_op_surface_test.go — real-user-journey tests for the management
// operations added to complete the DynamoDB control-plane surface: Kinesis
// streaming destinations, Contributor Insights, version-2017 global tables,
// and the account-level DescribeLimits/DescribeEndpoints. Each drives the
// genuine aws-sdk-go-v2 DynamoDB client against the emulator's HTTP server.
package dynamodb_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDDBKinesisStreamingDestination: enable a Kinesis destination on a table,
// describe it (ACTIVE), disable it (DISABLED), and observe the typed 404 for a
// missing table.
func TestDDBKinesisStreamingDestination(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "events", "id", "")

	const streamArn = "arn:aws:kinesis:us-east-1:000000000000:stream/events-stream"

	en, err := client.EnableKinesisStreamingDestination(ctx, &dynamodb.EnableKinesisStreamingDestinationInput{
		TableName: aws.String("events"),
		StreamArn: aws.String(streamArn),
	})
	require.NoError(t, err)
	assert.Equal(t, ddbtypes.DestinationStatusActive, en.DestinationStatus)
	assert.Equal(t, streamArn, aws.ToString(en.StreamArn))

	desc, err := client.DescribeKinesisStreamingDestination(ctx, &dynamodb.DescribeKinesisStreamingDestinationInput{
		TableName: aws.String("events"),
	})
	require.NoError(t, err)
	require.Len(t, desc.KinesisDataStreamDestinations, 1)
	assert.Equal(t, streamArn, aws.ToString(desc.KinesisDataStreamDestinations[0].StreamArn))
	assert.Equal(t, ddbtypes.DestinationStatusActive, desc.KinesisDataStreamDestinations[0].DestinationStatus)

	dis, err := client.DisableKinesisStreamingDestination(ctx, &dynamodb.DisableKinesisStreamingDestinationInput{
		TableName: aws.String("events"),
		StreamArn: aws.String(streamArn),
	})
	require.NoError(t, err)
	assert.Equal(t, ddbtypes.DestinationStatusDisabled, dis.DestinationStatus)

	var rnf *ddbtypes.ResourceNotFoundException
	_, err = client.DescribeKinesisStreamingDestination(ctx, &dynamodb.DescribeKinesisStreamingDestinationInput{
		TableName: aws.String("ghost"),
	})
	require.ErrorAs(t, err, &rnf)
}

// TestDDBContributorInsights: enable Contributor Insights for a table, describe
// it (ENABLED), list it, disable it (DISABLED), and observe the typed 404 for a
// missing table.
func TestDDBContributorInsights(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "metrics", "id", "")

	up, err := client.UpdateContributorInsights(ctx, &dynamodb.UpdateContributorInsightsInput{
		TableName:                 aws.String("metrics"),
		ContributorInsightsAction: ddbtypes.ContributorInsightsActionEnable,
	})
	require.NoError(t, err)
	assert.Equal(t, ddbtypes.ContributorInsightsStatusEnabled, up.ContributorInsightsStatus)

	desc, err := client.DescribeContributorInsights(ctx, &dynamodb.DescribeContributorInsightsInput{
		TableName: aws.String("metrics"),
	})
	require.NoError(t, err)
	assert.Equal(t, ddbtypes.ContributorInsightsStatusEnabled, desc.ContributorInsightsStatus)

	list, err := client.ListContributorInsights(ctx, &dynamodb.ListContributorInsightsInput{})
	require.NoError(t, err)
	require.Len(t, list.ContributorInsightsSummaries, 1)
	assert.Equal(t, "metrics", aws.ToString(list.ContributorInsightsSummaries[0].TableName))

	dis, err := client.UpdateContributorInsights(ctx, &dynamodb.UpdateContributorInsightsInput{
		TableName:                 aws.String("metrics"),
		ContributorInsightsAction: ddbtypes.ContributorInsightsActionDisable,
	})
	require.NoError(t, err)
	assert.Equal(t, ddbtypes.ContributorInsightsStatusDisabled, dis.ContributorInsightsStatus)

	var rnf *ddbtypes.ResourceNotFoundException
	_, err = client.DescribeContributorInsights(ctx, &dynamodb.DescribeContributorInsightsInput{
		TableName: aws.String("ghost"),
	})
	require.ErrorAs(t, err, &rnf)
}

// TestDDBGlobalTableLifecycle: create a version-2017 global table over an
// existing table, describe/list it, add a replica via UpdateGlobalTable, and
// observe the typed errors for a missing global table and a missing underlying
// table.
func TestDDBGlobalTableLifecycle(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "sessions", "id", "")

	cr, err := client.CreateGlobalTable(ctx, &dynamodb.CreateGlobalTableInput{
		GlobalTableName: aws.String("sessions"),
		ReplicationGroup: []ddbtypes.Replica{
			{RegionName: aws.String("us-east-1")},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "sessions", aws.ToString(cr.GlobalTableDescription.GlobalTableName))
	require.Len(t, cr.GlobalTableDescription.ReplicationGroup, 1)

	desc, err := client.DescribeGlobalTable(ctx, &dynamodb.DescribeGlobalTableInput{
		GlobalTableName: aws.String("sessions"),
	})
	require.NoError(t, err)
	assert.Equal(t, ddbtypes.GlobalTableStatusActive, desc.GlobalTableDescription.GlobalTableStatus)

	list, err := client.ListGlobalTables(ctx, &dynamodb.ListGlobalTablesInput{})
	require.NoError(t, err)
	require.Len(t, list.GlobalTables, 1)
	assert.Equal(t, "sessions", aws.ToString(list.GlobalTables[0].GlobalTableName))

	upd, err := client.UpdateGlobalTable(ctx, &dynamodb.UpdateGlobalTableInput{
		GlobalTableName: aws.String("sessions"),
		ReplicaUpdates: []ddbtypes.ReplicaUpdate{
			{Create: &ddbtypes.CreateReplicaAction{RegionName: aws.String("us-west-2")}},
		},
	})
	require.NoError(t, err)
	assert.Len(t, upd.GlobalTableDescription.ReplicationGroup, 2)

	// Describe on a global table that was never created is a typed 404.
	var gtnf *ddbtypes.GlobalTableNotFoundException
	_, err = client.DescribeGlobalTable(ctx, &dynamodb.DescribeGlobalTableInput{
		GlobalTableName: aws.String("nope"),
	})
	require.ErrorAs(t, err, &gtnf)

	// CreateGlobalTable over a table that does not exist is a typed
	// TableNotFoundException.
	var tnf *ddbtypes.TableNotFoundException
	_, err = client.CreateGlobalTable(ctx, &dynamodb.CreateGlobalTableInput{
		GlobalTableName:  aws.String("ghost"),
		ReplicationGroup: []ddbtypes.Replica{{RegionName: aws.String("us-east-1")}},
	})
	require.ErrorAs(t, err, &tnf)

	// A duplicate global table is a typed GlobalTableAlreadyExistsException.
	var exists *ddbtypes.GlobalTableAlreadyExistsException
	_, err = client.CreateGlobalTable(ctx, &dynamodb.CreateGlobalTableInput{
		GlobalTableName:  aws.String("sessions"),
		ReplicationGroup: []ddbtypes.Replica{{RegionName: aws.String("us-east-1")}},
	})
	require.ErrorAs(t, err, &exists)
}

// TestDDBDescribeEndpoints: DescribeEndpoints reports the emulator's own host so
// an endpoint-discovery client keeps talking to it.
func TestDDBDescribeEndpoints(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	out, err := client.DescribeEndpoints(ctx, &dynamodb.DescribeEndpointsInput{})
	require.NoError(t, err)
	require.Len(t, out.Endpoints, 1)
	assert.NotEmpty(t, aws.ToString(out.Endpoints[0].Address))
	assert.Positive(t, out.Endpoints[0].CachePeriodInMinutes)
}

// TestDDBContributorInsightsMissingIndex: Describe on a non-existent index of an
// existing table is a typed ResourceNotFoundException.
func TestDDBContributorInsightsMissingIndex(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "idxless", "id", "")

	_, err := client.DescribeContributorInsights(ctx, &dynamodb.DescribeContributorInsightsInput{
		TableName: aws.String("idxless"),
		IndexName: aws.String("no-such-index"),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	require.True(t, errors.As(err, &apiErr))
	assert.Equal(t, "ResourceNotFoundException", apiErr.ErrorCode())
}
