// streams_test.go — real-user-journey tests that drive the genuine
// aws-sdk-go-v2 DynamoDB and DynamoDB Streams clients against the emulator's
// HTTP server (httptest). A table is created with a stream enabled, items are
// written through the real DynamoDB client, and the change records are read back
// through the real DynamoDB Streams client (DescribeStream -> GetShardIterator
// -> GetRecords). Assertions are on SDK-decoded responses, not raw HTTP.
package dynamodb_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodbstreams"
	streamtypes "github.com/aws/aws-sdk-go-v2/service/dynamodbstreams/types"
	"github.com/aws/smithy-go"
	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newStreamsEnv wires a DynamoDB + DynamoDB Streams SDK pair at one emulator
// server, so items written through the DynamoDB client surface as change records
// on the Streams client.
func newStreamsEnv(t *testing.T) (*dynamodb.Client, *dynamodbstreams.Client) {
	t.Helper()

	provider := cloudemu.NewAWS()
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

	httpClient := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

	ddb := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
		o.HTTPClient = httpClient
	})
	streams := dynamodbstreams.NewFromConfig(cfg, func(o *dynamodbstreams.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
		o.HTTPClient = httpClient
	})

	return ddb, streams
}

// createStreamTable creates a table (pk HASH string, sk RANGE number) with a
// stream at the given view type and returns its LatestStreamArn.
func createStreamTable(t *testing.T, ddb *dynamodb.Client, table string, view ddbtypes.StreamViewType) string {
	t.Helper()

	out, err := ddb.CreateTable(context.Background(), &dynamodb.CreateTableInput{
		TableName: aws.String(table),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("sk"), AttributeType: ddbtypes.ScalarAttributeTypeN},
		},
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash},
			{AttributeName: aws.String("sk"), KeyType: ddbtypes.KeyTypeRange},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
		StreamSpecification: &ddbtypes.StreamSpecification{
			StreamEnabled:  aws.Bool(true),
			StreamViewType: view,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, out.TableDescription.LatestStreamArn)

	return aws.ToString(out.TableDescription.LatestStreamArn)
}

func putItem(t *testing.T, ddb *dynamodb.Client, table, pk, sk, val string) {
	t.Helper()

	_, err := ddb.PutItem(context.Background(), &dynamodb.PutItemInput{
		TableName: aws.String(table),
		Item: map[string]ddbtypes.AttributeValue{
			"pk":  &ddbtypes.AttributeValueMemberS{Value: pk},
			"sk":  &ddbtypes.AttributeValueMemberN{Value: sk},
			"val": &ddbtypes.AttributeValueMemberS{Value: val},
		},
	})
	require.NoError(t, err)
}

// trimHorizonIterator resolves the sole shard and returns a TRIM_HORIZON shard
// iterator for it.
func trimHorizonIterator(t *testing.T, streams *dynamodbstreams.Client, streamArn string) string {
	t.Helper()

	return shardIterator(t, streams, streamArn, streamtypes.ShardIteratorTypeTrimHorizon, "")
}

func shardIterator(
	t *testing.T,
	streams *dynamodbstreams.Client,
	streamArn string,
	iterType streamtypes.ShardIteratorType,
	seq string,
) string {
	t.Helper()

	desc, err := streams.DescribeStream(context.Background(), &dynamodbstreams.DescribeStreamInput{
		StreamArn: aws.String(streamArn),
	})
	require.NoError(t, err)
	require.Len(t, desc.StreamDescription.Shards, 1)

	in := &dynamodbstreams.GetShardIteratorInput{
		StreamArn:         aws.String(streamArn),
		ShardId:           desc.StreamDescription.Shards[0].ShardId,
		ShardIteratorType: iterType,
	}
	if seq != "" {
		in.SequenceNumber = aws.String(seq)
	}

	it, err := streams.GetShardIterator(context.Background(), in)
	require.NoError(t, err)

	return aws.ToString(it.ShardIterator)
}

func TestStreamsListStreams(t *testing.T) {
	t.Parallel()

	ddb, streams := newStreamsEnv(t)
	arn := createStreamTable(t, ddb, "orders", ddbtypes.StreamViewTypeNewAndOldImages)

	out, err := streams.ListStreams(context.Background(), &dynamodbstreams.ListStreamsInput{})
	require.NoError(t, err)
	require.Len(t, out.Streams, 1)
	assert.Equal(t, arn, aws.ToString(out.Streams[0].StreamArn))
	assert.Equal(t, "orders", aws.ToString(out.Streams[0].TableName))
	assert.NotEmpty(t, aws.ToString(out.Streams[0].StreamLabel))

	// TableName filter narrows the result.
	filtered, err := streams.ListStreams(context.Background(), &dynamodbstreams.ListStreamsInput{
		TableName: aws.String("orders"),
	})
	require.NoError(t, err)
	require.Len(t, filtered.Streams, 1)

	none, err := streams.ListStreams(context.Background(), &dynamodbstreams.ListStreamsInput{
		TableName: aws.String("missing"),
	})
	require.NoError(t, err)
	assert.Empty(t, none.Streams)
}

func TestStreamsDescribeStream(t *testing.T) {
	t.Parallel()

	ddb, streams := newStreamsEnv(t)
	arn := createStreamTable(t, ddb, "orders", ddbtypes.StreamViewTypeNewAndOldImages)

	desc, err := streams.DescribeStream(context.Background(), &dynamodbstreams.DescribeStreamInput{
		StreamArn: aws.String(arn),
	})
	require.NoError(t, err)

	sd := desc.StreamDescription
	assert.Equal(t, arn, aws.ToString(sd.StreamArn))
	assert.Equal(t, streamtypes.StreamStatusEnabled, sd.StreamStatus)
	assert.Equal(t, streamtypes.StreamViewTypeNewAndOldImages, sd.StreamViewType)
	assert.Equal(t, "orders", aws.ToString(sd.TableName))
	assert.NotEmpty(t, aws.ToString(sd.StreamLabel))

	require.Len(t, sd.KeySchema, 2)
	assert.Equal(t, "pk", aws.ToString(sd.KeySchema[0].AttributeName))
	assert.Equal(t, streamtypes.KeyTypeHash, sd.KeySchema[0].KeyType)
	assert.Equal(t, "sk", aws.ToString(sd.KeySchema[1].AttributeName))
	assert.Equal(t, streamtypes.KeyTypeRange, sd.KeySchema[1].KeyType)

	require.Len(t, sd.Shards, 1)
	assert.NotNil(t, sd.Shards[0].SequenceNumberRange.StartingSequenceNumber)
	// An open shard has no ending sequence number.
	assert.Nil(t, sd.Shards[0].SequenceNumberRange.EndingSequenceNumber)
}

func TestStreamsGetRecordsTrimHorizon(t *testing.T) {
	t.Parallel()

	ddb, streams := newStreamsEnv(t)
	arn := createStreamTable(t, ddb, "orders", ddbtypes.StreamViewTypeNewAndOldImages)

	// INSERT, MODIFY, REMOVE on the same key produce three ordered records.
	putItem(t, ddb, "orders", "a", "1", "first")
	putItem(t, ddb, "orders", "a", "1", "second")
	_, err := ddb.DeleteItem(context.Background(), &dynamodb.DeleteItemInput{
		TableName: aws.String("orders"),
		Key: map[string]ddbtypes.AttributeValue{
			"pk": &ddbtypes.AttributeValueMemberS{Value: "a"},
			"sk": &ddbtypes.AttributeValueMemberN{Value: "1"},
		},
	})
	require.NoError(t, err)

	it := trimHorizonIterator(t, streams, arn)
	recs, err := streams.GetRecords(context.Background(), &dynamodbstreams.GetRecordsInput{
		ShardIterator: aws.String(it),
	})
	require.NoError(t, err)
	require.Len(t, recs.Records, 3)
	require.NotNil(t, recs.NextShardIterator, "open shard always returns a next iterator")

	insert, modify, remove := recs.Records[0], recs.Records[1], recs.Records[2]
	assert.Equal(t, streamtypes.OperationTypeInsert, insert.EventName)
	assert.Equal(t, streamtypes.OperationTypeModify, modify.EventName)
	assert.Equal(t, streamtypes.OperationTypeRemove, remove.EventName)

	for _, rec := range recs.Records {
		assert.Equal(t, "aws:dynamodb", aws.ToString(rec.EventSource))
		assert.Equal(t, "us-east-1", aws.ToString(rec.AwsRegion))
		assert.NotEmpty(t, aws.ToString(rec.EventID))
		require.NotNil(t, rec.Dynamodb)
		// Keys are always present regardless of view type.
		assert.Contains(t, rec.Dynamodb.Keys, "pk")
		assert.Contains(t, rec.Dynamodb.Keys, "sk")
	}

	// INSERT carries a NewImage; MODIFY carries both; REMOVE carries the OldImage.
	assertS(t, insert.Dynamodb.NewImage["val"], "first")
	assert.Nil(t, insert.Dynamodb.OldImage)
	assertS(t, modify.Dynamodb.NewImage["val"], "second")
	assertS(t, modify.Dynamodb.OldImage["val"], "first")
	assert.Nil(t, remove.Dynamodb.NewImage)
	assertS(t, remove.Dynamodb.OldImage["val"], "second")
}

func TestStreamsGetShardIteratorLatest(t *testing.T) {
	t.Parallel()

	ddb, streams := newStreamsEnv(t)
	arn := createStreamTable(t, ddb, "orders", ddbtypes.StreamViewTypeNewImage)

	// Records written BEFORE the LATEST iterator is minted must not appear.
	putItem(t, ddb, "orders", "a", "1", "old")

	it := shardIterator(t, streams, arn, streamtypes.ShardIteratorTypeLatest, "")

	// Records written AFTER the iterator was minted must appear.
	putItem(t, ddb, "orders", "b", "2", "new")

	recs, err := streams.GetRecords(context.Background(), &dynamodbstreams.GetRecordsInput{
		ShardIterator: aws.String(it),
	})
	require.NoError(t, err)
	require.Len(t, recs.Records, 1)
	assertS(t, recs.Records[0].Dynamodb.Keys["pk"], "b")
	assertS(t, recs.Records[0].Dynamodb.NewImage["val"], "new")
}

func TestStreamsGetRecordsPagination(t *testing.T) {
	t.Parallel()

	ddb, streams := newStreamsEnv(t)
	arn := createStreamTable(t, ddb, "orders", ddbtypes.StreamViewTypeKeysOnly)

	for _, sk := range []string{"1", "2", "3", "4", "5"} {
		putItem(t, ddb, "orders", "a", sk, "v")
	}

	it := trimHorizonIterator(t, streams, arn)

	// First page: Limit=2 returns 2 records and an iterator to continue.
	page1, err := streams.GetRecords(context.Background(), &dynamodbstreams.GetRecordsInput{
		ShardIterator: aws.String(it),
		Limit:         aws.Int32(2),
	})
	require.NoError(t, err)
	require.Len(t, page1.Records, 2)
	require.NotNil(t, page1.NextShardIterator)

	// Drain the rest through the returned iterator.
	drained := len(page1.Records)
	next := page1.NextShardIterator

	for range 5 {
		page, gerr := streams.GetRecords(context.Background(), &dynamodbstreams.GetRecordsInput{
			ShardIterator: next,
			Limit:         aws.Int32(2),
		})
		require.NoError(t, gerr)

		drained += len(page.Records)
		next = page.NextShardIterator

		if len(page.Records) == 0 {
			break
		}
	}

	assert.Equal(t, 5, drained)
}

func TestStreamsKeysOnlyOmitsImages(t *testing.T) {
	t.Parallel()

	ddb, streams := newStreamsEnv(t)
	arn := createStreamTable(t, ddb, "orders", ddbtypes.StreamViewTypeKeysOnly)

	putItem(t, ddb, "orders", "a", "1", "v")

	it := trimHorizonIterator(t, streams, arn)
	recs, err := streams.GetRecords(context.Background(), &dynamodbstreams.GetRecordsInput{
		ShardIterator: aws.String(it),
	})
	require.NoError(t, err)
	require.Len(t, recs.Records, 1)

	rec := recs.Records[0]
	assert.Equal(t, streamtypes.StreamViewTypeKeysOnly, rec.Dynamodb.StreamViewType)
	assert.Nil(t, rec.Dynamodb.NewImage)
	assert.Nil(t, rec.Dynamodb.OldImage)
	assert.Contains(t, rec.Dynamodb.Keys, "pk")
}

func TestStreamsAtSequenceNumberInclusive(t *testing.T) {
	t.Parallel()

	ddb, streams := newStreamsEnv(t)
	arn := createStreamTable(t, ddb, "orders", ddbtypes.StreamViewTypeNewImage)

	putItem(t, ddb, "orders", "a", "1", "one")
	putItem(t, ddb, "orders", "a", "2", "two")

	// Read all to learn the second record's sequence number.
	itAll := trimHorizonIterator(t, streams, arn)
	all, err := streams.GetRecords(context.Background(), &dynamodbstreams.GetRecordsInput{
		ShardIterator: aws.String(itAll),
	})
	require.NoError(t, err)
	require.Len(t, all.Records, 2)
	secondSeq := aws.ToString(all.Records[1].Dynamodb.SequenceNumber)

	// AT_SEQUENCE_NUMBER on the second record includes that record itself.
	itAt := shardIterator(t, streams, arn, streamtypes.ShardIteratorTypeAtSequenceNumber, secondSeq)
	atRecs, err := streams.GetRecords(context.Background(), &dynamodbstreams.GetRecordsInput{
		ShardIterator: aws.String(itAt),
	})
	require.NoError(t, err)
	require.Len(t, atRecs.Records, 1)
	assert.Equal(t, secondSeq, aws.ToString(atRecs.Records[0].Dynamodb.SequenceNumber))
	assertS(t, atRecs.Records[0].Dynamodb.NewImage["val"], "two")

	// AFTER_SEQUENCE_NUMBER on the second record excludes it (and is the tip).
	itAfter := shardIterator(t, streams, arn, streamtypes.ShardIteratorTypeAfterSequenceNumber, secondSeq)
	afterRecs, err := streams.GetRecords(context.Background(), &dynamodbstreams.GetRecordsInput{
		ShardIterator: aws.String(itAfter),
	})
	require.NoError(t, err)
	assert.Empty(t, afterRecs.Records)
}

func TestStreamsUnknownStreamArn(t *testing.T) {
	t.Parallel()

	ddb, streams := newStreamsEnv(t)
	_ = createStreamTable(t, ddb, "orders", ddbtypes.StreamViewTypeNewImage)

	_, err := streams.DescribeStream(context.Background(), &dynamodbstreams.DescribeStreamInput{
		StreamArn: aws.String("arn:aws:dynamodb:us-east-1:000000000000:table/ghost/stream/2020-01-01T00:00:00.000"),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, "ResourceNotFoundException", apiErr.ErrorCode())
}

func TestStreamsDisabledStreamNotFound(t *testing.T) {
	t.Parallel()

	ddb, streams := newStreamsEnv(t)

	// A table without a stream produces no listable stream and its (empty) ARN
	// resolves to ResourceNotFoundException.
	_, err := ddb.CreateTable(context.Background(), &dynamodb.CreateTableInput{
		TableName: aws.String("nostream"),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)

	list, err := streams.ListStreams(context.Background(), &dynamodbstreams.ListStreamsInput{})
	require.NoError(t, err)
	assert.Empty(t, list.Streams)

	_, err = streams.DescribeStream(context.Background(), &dynamodbstreams.DescribeStreamInput{
		StreamArn: aws.String("arn:aws:dynamodb:us-east-1:000000000000:table/nostream/stream/x"),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, "ResourceNotFoundException", apiErr.ErrorCode())
}

// TestStreamsRouteDisjointFromControlPlane verifies the control-plane DynamoDB
// client still works (DynamoDB_20120810.* hits the control-plane handler) while
// the Streams client hits the streams handler, against the same server.
func TestStreamsRouteDisjointFromControlPlane(t *testing.T) {
	t.Parallel()

	ddb, streams := newStreamsEnv(t)
	arn := createStreamTable(t, ddb, "orders", ddbtypes.StreamViewTypeNewImage)

	// Control-plane op still routes to the DynamoDB handler.
	dt, err := ddb.DescribeTable(context.Background(), &dynamodb.DescribeTableInput{
		TableName: aws.String("orders"),
	})
	require.NoError(t, err)
	assert.Equal(t, arn, aws.ToString(dt.Table.LatestStreamArn))

	// Streams op routes to the streams handler.
	ls, err := streams.ListStreams(context.Background(), &dynamodbstreams.ListStreamsInput{})
	require.NoError(t, err)
	require.Len(t, ls.Streams, 1)
}

// assertS asserts a stream AttributeValue is a string with the expected value.
func assertS(t *testing.T, av streamtypes.AttributeValue, want string) {
	t.Helper()

	s, ok := av.(*streamtypes.AttributeValueMemberS)
	require.True(t, ok, "expected string AttributeValue, got %T", av)
	assert.Equal(t, want, s.Value)
}
