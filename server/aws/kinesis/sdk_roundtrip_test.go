package kinesis_test

import (
	"context"
	"errors"
	"math/big"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awskinesis "github.com/aws/aws-sdk-go-v2/service/kinesis"
	kinesistypes "github.com/aws/aws-sdk-go-v2/service/kinesis/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newKinesisClient(t *testing.T) *awskinesis.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{Kinesis: cloud.Kinesis})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awskinesis.NewFromConfig(cfg, func(o *awskinesis.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKStreamLifecycleAndRecords(t *testing.T) {
	ctx := context.Background()
	c := newKinesisClient(t)

	if _, err := c.CreateStream(ctx, &awskinesis.CreateStreamInput{
		StreamName: aws.String("orders"),
		ShardCount: aws.Int32(2),
		Tags:       map[string]string{"env": "test"},
	}); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}

	desc, err := c.DescribeStream(ctx, &awskinesis.DescribeStreamInput{StreamName: aws.String("orders")})
	if err != nil {
		t.Fatalf("DescribeStream: %v", err)
	}

	if desc.StreamDescription.StreamStatus != kinesistypes.StreamStatusActive {
		t.Fatalf("status = %s, want ACTIVE", desc.StreamDescription.StreamStatus)
	}

	if len(desc.StreamDescription.Shards) != 2 {
		t.Fatalf("want 2 shards, got %d", len(desc.StreamDescription.Shards))
	}

	// Put a record, then read it back through an iterator.
	pr, err := c.PutRecord(ctx, &awskinesis.PutRecordInput{
		StreamName:   aws.String("orders"),
		PartitionKey: aws.String("customer-1"),
		Data:         []byte("hello-kinesis"),
	})
	if err != nil {
		t.Fatalf("PutRecord: %v", err)
	}

	if aws.ToString(pr.ShardId) == "" || aws.ToString(pr.SequenceNumber) == "" {
		t.Fatalf("PutRecord returned empty shard/seq: %+v", pr)
	}

	it, err := c.GetShardIterator(ctx, &awskinesis.GetShardIteratorInput{
		StreamName:        aws.String("orders"),
		ShardId:           pr.ShardId,
		ShardIteratorType: kinesistypes.ShardIteratorTypeTrimHorizon,
	})
	if err != nil {
		t.Fatalf("GetShardIterator: %v", err)
	}

	gr, err := c.GetRecords(ctx, &awskinesis.GetRecordsInput{ShardIterator: it.ShardIterator})
	if err != nil {
		t.Fatalf("GetRecords: %v", err)
	}

	if len(gr.Records) != 1 || string(gr.Records[0].Data) != "hello-kinesis" {
		t.Fatalf("unexpected records: %+v", gr.Records)
	}

	if aws.ToString(gr.Records[0].PartitionKey) != "customer-1" {
		t.Fatalf("partition key = %s", aws.ToString(gr.Records[0].PartitionKey))
	}
}

func TestSDKPutRecordsBatch(t *testing.T) {
	ctx := context.Background()
	c := newKinesisClient(t)

	mustCreate(t, c, "batch", 3)

	out, err := c.PutRecords(ctx, &awskinesis.PutRecordsInput{
		StreamName: aws.String("batch"),
		Records: []kinesistypes.PutRecordsRequestEntry{
			{PartitionKey: aws.String("a"), Data: []byte("one")},
			{PartitionKey: aws.String("b"), Data: []byte("two")},
			{PartitionKey: aws.String("c"), Data: []byte("three")},
		},
	})
	if err != nil {
		t.Fatalf("PutRecords: %v", err)
	}

	if aws.ToInt32(out.FailedRecordCount) != 0 {
		t.Fatalf("want 0 failed, got %d", aws.ToInt32(out.FailedRecordCount))
	}

	if len(out.Records) != 3 {
		t.Fatalf("want 3 result entries, got %d", len(out.Records))
	}

	for i, r := range out.Records {
		if aws.ToString(r.SequenceNumber) == "" || aws.ToString(r.ShardId) == "" {
			t.Fatalf("entry %d missing seq/shard: %+v", i, r)
		}
	}
}

func TestSDKConsumersAndTags(t *testing.T) {
	ctx := context.Background()
	c := newKinesisClient(t)

	mustCreate(t, c, "events", 1)

	sum, err := c.DescribeStreamSummary(ctx, &awskinesis.DescribeStreamSummaryInput{StreamName: aws.String("events")})
	if err != nil {
		t.Fatalf("DescribeStreamSummary: %v", err)
	}

	arn := sum.StreamDescriptionSummary.StreamARN

	reg, err := c.RegisterStreamConsumer(ctx, &awskinesis.RegisterStreamConsumerInput{
		StreamARN:    arn,
		ConsumerName: aws.String("analytics"),
	})
	if err != nil {
		t.Fatalf("RegisterStreamConsumer: %v", err)
	}

	if reg.Consumer.ConsumerStatus != kinesistypes.ConsumerStatusActive {
		t.Fatalf("consumer status = %s", reg.Consumer.ConsumerStatus)
	}

	list, err := c.ListStreamConsumers(ctx, &awskinesis.ListStreamConsumersInput{StreamARN: arn})
	if err != nil {
		t.Fatalf("ListStreamConsumers: %v", err)
	}

	if len(list.Consumers) != 1 {
		t.Fatalf("want 1 consumer, got %d", len(list.Consumers))
	}

	// Deleting a stream with a consumer requires EnforceConsumerDeletion.
	if _, err := c.DeleteStream(ctx, &awskinesis.DeleteStreamInput{StreamName: aws.String("events")}); err == nil {
		t.Fatal("DeleteStream with consumer should fail without EnforceConsumerDeletion")
	}

	// Tags.
	if _, err := c.AddTagsToStream(ctx, &awskinesis.AddTagsToStreamInput{
		StreamName: aws.String("events"),
		Tags:       map[string]string{"team": "data"},
	}); err != nil {
		t.Fatalf("AddTagsToStream: %v", err)
	}

	tags, err := c.ListTagsForStream(ctx, &awskinesis.ListTagsForStreamInput{StreamName: aws.String("events")})
	if err != nil {
		t.Fatalf("ListTagsForStream: %v", err)
	}

	if len(tags.Tags) != 1 || aws.ToString(tags.Tags[0].Key) != "team" {
		t.Fatalf("unexpected tags: %+v", tags.Tags)
	}
}

func TestSDKSplitShardIncreasesShardCount(t *testing.T) {
	ctx := context.Background()
	c := newKinesisClient(t)

	mustCreate(t, c, "reshard", 1)

	desc, err := c.DescribeStream(ctx, &awskinesis.DescribeStreamInput{StreamName: aws.String("reshard")})
	if err != nil {
		t.Fatalf("DescribeStream: %v", err)
	}

	shard := desc.StreamDescription.Shards[0]
	// Split at the midpoint of the shard's hash-key range.
	mid := midpoint(t, aws.ToString(shard.HashKeyRange.StartingHashKey), aws.ToString(shard.HashKeyRange.EndingHashKey))

	if _, err := c.SplitShard(ctx, &awskinesis.SplitShardInput{
		StreamName:         aws.String("reshard"),
		ShardToSplit:       shard.ShardId,
		NewStartingHashKey: aws.String(mid),
	}); err != nil {
		t.Fatalf("SplitShard: %v", err)
	}

	ls, err := c.ListShards(ctx, &awskinesis.ListShardsInput{StreamName: aws.String("reshard")})
	if err != nil {
		t.Fatalf("ListShards: %v", err)
	}

	// 1 original (now closed) + 2 children = 3 total.
	if len(ls.Shards) != 3 {
		t.Fatalf("want 3 shards after split, got %d", len(ls.Shards))
	}
}

func TestSDKErrorsAreTyped(t *testing.T) {
	ctx := context.Background()
	c := newKinesisClient(t)

	_, err := c.DescribeStream(ctx, &awskinesis.DescribeStreamInput{StreamName: aws.String("nope")})
	if err == nil {
		t.Fatal("DescribeStream on missing stream should fail")
	}

	var nf *kinesistypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("want ResourceNotFoundException, got %T: %v", err, err)
	}

	// Duplicate create → ResourceInUseException.
	mustCreate(t, c, "dup", 1)

	_, err = c.CreateStream(ctx, &awskinesis.CreateStreamInput{StreamName: aws.String("dup"), ShardCount: aws.Int32(1)})
	if err == nil {
		t.Fatal("duplicate CreateStream should fail")
	}

	var inUse *kinesistypes.ResourceInUseException
	if !errors.As(err, &inUse) {
		t.Fatalf("want ResourceInUseException, got %T: %v", err, err)
	}
}

func midpoint(t *testing.T, start, end string) string {
	t.Helper()

	s, ok1 := new(big.Int).SetString(start, 10)
	e, ok2 := new(big.Int).SetString(end, 10)

	if !ok1 || !ok2 {
		t.Fatalf("bad hash keys %q %q", start, end)
	}

	mid := new(big.Int).Add(s, e)
	mid.Div(mid, big.NewInt(2))
	// The new starting hash key must be > start, so bump when the average floors to start.
	if mid.Cmp(s) <= 0 {
		mid.Add(s, big.NewInt(1))
	}

	return mid.String()
}

func mustCreate(t *testing.T, c *awskinesis.Client, name string, shards int32) {
	t.Helper()

	if _, err := c.CreateStream(context.Background(), &awskinesis.CreateStreamInput{
		StreamName: aws.String(name),
		ShardCount: aws.Int32(shards),
	}); err != nil {
		t.Fatalf("CreateStream(%s): %v", name, err)
	}
}
