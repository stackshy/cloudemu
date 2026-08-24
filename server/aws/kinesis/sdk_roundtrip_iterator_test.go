package kinesis_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskinesis "github.com/aws/aws-sdk-go-v2/service/kinesis"
	kinesistypes "github.com/aws/aws-sdk-go-v2/service/kinesis/types"
	"github.com/aws/smithy-go"
)

// TestSDKGetShardIteratorAtTimestampWithoutTimestamp confirms that requesting an
// AT_TIMESTAMP iterator without a Timestamp is rejected with
// InvalidArgumentException (HTTP 400), matching real Kinesis, instead of
// silently behaving like TRIM_HORIZON.
func TestSDKGetShardIteratorAtTimestampWithoutTimestamp(t *testing.T) {
	ctx := context.Background()
	c := newKinesisClient(t)

	if _, err := c.CreateStream(ctx, &awskinesis.CreateStreamInput{
		StreamName: aws.String("ts-stream"),
		ShardCount: aws.Int32(1),
	}); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}

	desc, err := c.DescribeStream(ctx, &awskinesis.DescribeStreamInput{StreamName: aws.String("ts-stream")})
	if err != nil {
		t.Fatalf("DescribeStream: %v", err)
	}

	shardID := desc.StreamDescription.Shards[0].ShardId

	_, err = c.GetShardIterator(ctx, &awskinesis.GetShardIteratorInput{
		StreamName:        aws.String("ts-stream"),
		ShardId:           shardID,
		ShardIteratorType: kinesistypes.ShardIteratorTypeAtTimestamp,
		// Timestamp intentionally omitted.
	})
	if err == nil {
		t.Fatal("GetShardIterator with AT_TIMESTAMP and no Timestamp returned nil error, want InvalidArgumentException")
	}

	var ae smithy.APIError
	if !errors.As(err, &ae) {
		t.Fatalf("error is not a smithy APIError: %v", err)
	}

	if ae.ErrorCode() != "InvalidArgumentException" {
		t.Fatalf("error code = %q, want InvalidArgumentException", ae.ErrorCode())
	}
}

// TestSDKGetShardIteratorAtTimestampWithTimestamp confirms the happy path still
// works when a Timestamp is supplied.
func TestSDKGetShardIteratorAtTimestampWithTimestamp(t *testing.T) {
	ctx := context.Background()
	c := newKinesisClient(t)

	if _, err := c.CreateStream(ctx, &awskinesis.CreateStreamInput{
		StreamName: aws.String("ts-ok-stream"),
		ShardCount: aws.Int32(1),
	}); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}

	desc, err := c.DescribeStream(ctx, &awskinesis.DescribeStreamInput{StreamName: aws.String("ts-ok-stream")})
	if err != nil {
		t.Fatalf("DescribeStream: %v", err)
	}

	it, err := c.GetShardIterator(ctx, &awskinesis.GetShardIteratorInput{
		StreamName:        aws.String("ts-ok-stream"),
		ShardId:           desc.StreamDescription.Shards[0].ShardId,
		ShardIteratorType: kinesistypes.ShardIteratorTypeAtTimestamp,
		Timestamp:         desc.StreamDescription.StreamCreationTimestamp,
	})
	if err != nil {
		t.Fatalf("GetShardIterator with Timestamp: %v", err)
	}

	if aws.ToString(it.ShardIterator) == "" {
		t.Fatal("GetShardIterator returned empty iterator")
	}
}
