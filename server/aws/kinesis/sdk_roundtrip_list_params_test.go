package kinesis_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskinesis "github.com/aws/aws-sdk-go-v2/service/kinesis"
	kinesistypes "github.com/aws/aws-sdk-go-v2/service/kinesis/types"
)

func TestSDKListStreamsExclusiveStartStreamName(t *testing.T) {
	ctx := context.Background()
	c := newKinesisClient(t)

	for _, n := range []string{"s-a", "s-b", "s-c", "s-d"} {
		mustCreate(t, c, n, 1)
	}

	out, err := c.ListStreams(ctx, &awskinesis.ListStreamsInput{
		ExclusiveStartStreamName: aws.String("s-b"),
	})
	if err != nil {
		t.Fatalf("ListStreams: %v", err)
	}

	if len(out.StreamNames) != 2 || out.StreamNames[0] != "s-c" || out.StreamNames[1] != "s-d" {
		t.Fatalf("ExclusiveStartStreamName=s-b returned %v, want [s-c s-d]", out.StreamNames)
	}
}

func TestSDKListShardsShardFilterAtLatest(t *testing.T) {
	ctx := context.Background()
	c := newKinesisClient(t)

	mustCreate(t, c, "merge", 2)

	desc, err := c.DescribeStream(ctx, &awskinesis.DescribeStreamInput{StreamName: aws.String("merge")})
	if err != nil {
		t.Fatalf("DescribeStream: %v", err)
	}

	shards := desc.StreamDescription.Shards
	if len(shards) != 2 {
		t.Fatalf("want 2 shards to merge, got %d", len(shards))
	}

	if _, err := c.MergeShards(ctx, &awskinesis.MergeShardsInput{
		StreamName:           aws.String("merge"),
		ShardToMerge:         shards[0].ShardId,
		AdjacentShardToMerge: shards[1].ShardId,
	}); err != nil {
		t.Fatalf("MergeShards: %v", err)
	}

	// Unfiltered: 2 closed parents + 1 open child.
	all, err := c.ListShards(ctx, &awskinesis.ListShardsInput{StreamName: aws.String("merge")})
	if err != nil {
		t.Fatalf("ListShards: %v", err)
	}

	if len(all.Shards) != 3 {
		t.Fatalf("unfiltered ListShards = %d shards, want 3", len(all.Shards))
	}

	// AT_LATEST: only the currently open child shard.
	latest, err := c.ListShards(ctx, &awskinesis.ListShardsInput{
		StreamName:  aws.String("merge"),
		ShardFilter: &kinesistypes.ShardFilter{Type: kinesistypes.ShardFilterTypeAtLatest},
	})
	if err != nil {
		t.Fatalf("ListShards AT_LATEST: %v", err)
	}

	if len(latest.Shards) != 1 {
		t.Fatalf("AT_LATEST = %d shards, want 1 open child", len(latest.Shards))
	}

	if latest.Shards[0].SequenceNumberRange.EndingSequenceNumber != nil {
		t.Fatalf("AT_LATEST returned a closed shard %s", aws.ToString(latest.Shards[0].ShardId))
	}
}
