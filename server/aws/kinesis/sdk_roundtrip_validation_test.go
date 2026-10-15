package kinesis_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskinesis "github.com/aws/aws-sdk-go-v2/service/kinesis"
	kinesistypes "github.com/aws/aws-sdk-go-v2/service/kinesis/types"
	"github.com/aws/smithy-go"
)

func wantAPIErrorCode(t *testing.T, err error, code string) {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
}

func TestSDKPartitionKeyOver256CharsIsValidationException(t *testing.T) {
	ctx := context.Background()
	c := newKinesisClient(t)
	mustCreate(t, c, "pk", 1)

	long := strings.Repeat("k", 257)

	_, err := c.PutRecord(ctx, &awskinesis.PutRecordInput{
		StreamName: aws.String("pk"), PartitionKey: aws.String(long), Data: []byte("d"),
	})
	wantAPIErrorCode(t, err, "ValidationException")

	_, err = c.PutRecords(ctx, &awskinesis.PutRecordsInput{
		StreamName: aws.String("pk"),
		Records: []kinesistypes.PutRecordsRequestEntry{
			{PartitionKey: aws.String("ok"), Data: []byte("a")},
			{PartitionKey: aws.String(long), Data: []byte("b")},
		},
	})
	wantAPIErrorCode(t, err, "ValidationException")

	if _, err = c.PutRecord(ctx, &awskinesis.PutRecordInput{
		StreamName: aws.String("pk"), PartitionKey: aws.String(strings.Repeat("k", 256)), Data: []byte("d"),
	}); err != nil {
		t.Fatalf("256-char key: %v", err)
	}
}

func TestSDKGetRecordsLimitOutOfRange(t *testing.T) {
	ctx := context.Background()
	c := newKinesisClient(t)
	mustCreate(t, c, "lim", 1)

	ls, err := c.ListShards(ctx, &awskinesis.ListShardsInput{StreamName: aws.String("lim")})
	if err != nil {
		t.Fatalf("ListShards: %v", err)
	}

	it, err := c.GetShardIterator(ctx, &awskinesis.GetShardIteratorInput{
		StreamName: aws.String("lim"), ShardId: ls.Shards[0].ShardId,
		ShardIteratorType: kinesistypes.ShardIteratorTypeTrimHorizon,
	})
	if err != nil {
		t.Fatalf("GetShardIterator: %v", err)
	}

	for _, bad := range []int32{0, 10001} {
		_, err = c.GetRecords(ctx, &awskinesis.GetRecordsInput{ShardIterator: it.ShardIterator, Limit: aws.Int32(bad)})
		wantAPIErrorCode(t, err, "InvalidArgumentException")
	}

	for _, ok := range []*int32{nil, aws.Int32(1), aws.Int32(10000)} {
		if _, err = c.GetRecords(ctx, &awskinesis.GetRecordsInput{ShardIterator: it.ShardIterator, Limit: ok}); err != nil {
			t.Fatalf("GetRecords(limit=%v): %v", aws.ToInt32(ok), err)
		}
	}
}
