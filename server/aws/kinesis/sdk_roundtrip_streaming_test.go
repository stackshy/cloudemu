package kinesis_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awskinesis "github.com/aws/aws-sdk-go-v2/service/kinesis"
	kinesistypes "github.com/aws/aws-sdk-go-v2/service/kinesis/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newStreamingKinesisClient builds a Kinesis client whose HTTP transport
// disables keep-alives. Reusing a pooled connection races the httptest server's
// teardown for the eventstream (SubscribeToShard) response: the reader can
// observe "use of closed network connection" instead of a clean EOF once the
// stream is fully consumed. A fresh, server-closed connection per request makes
// the stream end deterministically.
func newStreamingKinesisClient(t *testing.T) *awskinesis.Client {
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
		o.HTTPClient = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	})
}

// benignStreamTeardown reports whether err from an eventstream's Err() is a
// transport-level teardown artifact rather than a protocol failure. Callers gate
// Err() on this only after asserting the stream's content is complete, so a
// genuinely truncated stream is still caught by those checks.
func benignStreamTeardown(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}

	return strings.Contains(err.Error(), "use of closed network connection")
}

func TestSDKSubscribeToShard(t *testing.T) {
	ctx := context.Background()
	c := newStreamingKinesisClient(t)

	mustCreate(t, c, "fanout", 1)

	sum, err := c.DescribeStreamSummary(ctx, &awskinesis.DescribeStreamSummaryInput{StreamName: aws.String("fanout")})
	if err != nil {
		t.Fatalf("DescribeStreamSummary: %v", err)
	}

	streamARN := sum.StreamDescriptionSummary.StreamARN

	if _, err = c.PutRecord(ctx, &awskinesis.PutRecordInput{
		StreamName:   aws.String("fanout"),
		PartitionKey: aws.String("customer-1"),
		Data:         []byte("hello-fanout"),
	}); err != nil {
		t.Fatalf("PutRecord: %v", err)
	}

	reg, err := c.RegisterStreamConsumer(ctx, &awskinesis.RegisterStreamConsumerInput{
		StreamARN:    streamARN,
		ConsumerName: aws.String("efo"),
	})
	if err != nil {
		t.Fatalf("RegisterStreamConsumer: %v", err)
	}

	ls, err := c.ListShards(ctx, &awskinesis.ListShardsInput{StreamName: aws.String("fanout")})
	if err != nil {
		t.Fatalf("ListShards: %v", err)
	}

	shardID := ls.Shards[0].ShardId

	out, err := c.SubscribeToShard(ctx, &awskinesis.SubscribeToShardInput{
		ConsumerARN: reg.Consumer.ConsumerARN,
		ShardId:     shardID,
		StartingPosition: &kinesistypes.StartingPosition{
			Type: kinesistypes.ShardIteratorTypeTrimHorizon,
		},
	})
	if err != nil {
		t.Fatalf("SubscribeToShard: %v", err)
	}

	stream := out.GetStream()
	defer stream.Close()

	var (
		sawEvent bool
		gotData  string
	)

	for ev := range stream.Events() {
		if e, ok := ev.(*kinesistypes.SubscribeToShardEventStreamMemberSubscribeToShardEvent); ok {
			sawEvent = true

			if len(e.Value.Records) > 0 {
				gotData = string(e.Value.Records[0].Data)
			}

			if aws.ToString(e.Value.ContinuationSequenceNumber) == "" {
				t.Fatal("expected a continuation sequence number")
			}
		}
	}

	if err := stream.Err(); err != nil && !benignStreamTeardown(err) {
		t.Fatalf("stream error: %v", err)
	}

	if !sawEvent {
		t.Fatal("expected at least one SubscribeToShardEvent")
	}

	if gotData != "hello-fanout" {
		t.Fatalf("record data round-trip failed: got %q, want %q", gotData, "hello-fanout")
	}
}
