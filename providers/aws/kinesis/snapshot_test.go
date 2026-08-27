package kinesis_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/providers/aws/kinesis"
	"github.com/stackshy/cloudemu/v2/services/kinesis/driver"
)

// TestSnapshotRoundTripKinesis proves a snapshot/restore round-trip preserves
// the Kinesis mock's state under the original identities: a stream (promoted
// from the unexported streamData) with its shards and stored records survives,
// the arn→name index still resolves an ARN to its stream, and a re-snapshot is
// byte-identical.
func TestSnapshotRoundTripKinesis(t *testing.T) {
	ctx := context.Background()
	src := newMock(t)

	if err := src.CreateStream(ctx, driver.CreateStreamInput{StreamName: "s-1", ShardCount: 1}); err != nil {
		t.Fatalf("create stream: %v", err)
	}

	sum, err := src.DescribeStreamSummary(ctx, "s-1", "")
	if err != nil {
		t.Fatalf("describe summary: %v", err)
	}

	if _, err := src.PutRecord(ctx, driver.PutRecordInput{
		StreamName: "s-1", Data: []byte("hello"), PartitionKey: "pk",
	}); err != nil {
		t.Fatalf("put record: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newMock(t)
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	// Stream survived, resolvable by name and by its ARN (the arn→name index).
	if _, err := dst.DescribeStreamSummary(ctx, "s-1", ""); err != nil {
		t.Fatalf("describe restored stream by name: %v", err)
	}

	byARN, err := dst.DescribeStreamSummary(ctx, "", sum.StreamARN)
	if err != nil {
		t.Fatalf("resolve restored stream by ARN: %v", err)
	}

	if byARN.StreamName != "s-1" {
		t.Fatalf("ARN resolved to %q, want s-1", byARN.StreamName)
	}

	assertRecordSurvived(ctx, t, dst)

	raw2, err := dst.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("re-snapshot: %v", err)
	}

	if !bytes.Equal(raw, raw2) {
		t.Fatalf("re-snapshot not byte-identical to original")
	}
}

// assertRecordSurvived reads the stream's single shard from TRIM_HORIZON and
// checks the put record's bytes came back, proving shards + records round-trip.
func assertRecordSurvived(ctx context.Context, t *testing.T, m *kinesis.Mock) {
	t.Helper()

	shards, err := m.ListShards(ctx, driver.ListShardsInput{StreamName: "s-1"})
	if err != nil {
		t.Fatalf("list restored shards: %v", err)
	}

	if len(shards.Shards) != 1 {
		t.Fatalf("restored shard count = %d, want 1", len(shards.Shards))
	}

	it, err := m.GetShardIterator(ctx, driver.GetShardIteratorInput{
		StreamName: "s-1", ShardID: shards.Shards[0].ShardID, ShardIteratorType: "TRIM_HORIZON",
	})
	if err != nil {
		t.Fatalf("get shard iterator: %v", err)
	}

	recs, err := m.GetRecords(ctx, it, 10)
	if err != nil {
		t.Fatalf("get records: %v", err)
	}

	if len(recs.Records) != 1 || !bytes.Equal(recs.Records[0].Data, []byte("hello")) {
		t.Fatalf("restored records = %+v, want one record with data 'hello'", recs.Records)
	}
}
