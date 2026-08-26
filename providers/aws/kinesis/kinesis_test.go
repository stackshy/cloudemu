package kinesis_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/kinesis"
	"github.com/stackshy/cloudemu/v2/services/kinesis/driver"
)

func newMock(t *testing.T) *kinesis.Mock {
	t.Helper()

	return kinesis.New(config.NewOptions())
}

func TestSequenceNumbersAreMonotonic(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	if err := m.CreateStream(ctx, driver.CreateStreamInput{StreamName: "s", ShardCount: 1}); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}

	var prev string

	for i := 0; i < 5; i++ {
		res, err := m.PutRecord(ctx, driver.PutRecordInput{
			StreamName: "s", PartitionKey: "k", Data: []byte("d"),
		})
		if err != nil {
			t.Fatalf("PutRecord: %v", err)
		}

		if prev != "" && res.SequenceNumber <= prev {
			t.Fatalf("sequence not increasing: %s then %s", prev, res.SequenceNumber)
		}

		prev = res.SequenceNumber
	}
}

func TestPartitionKeyRoutingIsDeterministic(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	if err := m.CreateStream(ctx, driver.CreateStreamInput{StreamName: "s", ShardCount: 4}); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}

	first, err := m.PutRecord(ctx, driver.PutRecordInput{StreamName: "s", PartitionKey: "same-key", Data: []byte("1")})
	if err != nil {
		t.Fatalf("PutRecord: %v", err)
	}

	second, err := m.PutRecord(ctx, driver.PutRecordInput{StreamName: "s", PartitionKey: "same-key", Data: []byte("2")})
	if err != nil {
		t.Fatalf("PutRecord: %v", err)
	}

	if first.ShardID != second.ShardID {
		t.Fatalf("same partition key routed to different shards: %s vs %s", first.ShardID, second.ShardID)
	}
}

func TestGetRecordsAdvancesIterator(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	if err := m.CreateStream(ctx, driver.CreateStreamInput{StreamName: "s", ShardCount: 1}); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}

	for i := 0; i < 3; i++ {
		if _, err := m.PutRecord(ctx, driver.PutRecordInput{StreamName: "s", PartitionKey: "k", Data: []byte{byte(i)}}); err != nil {
			t.Fatalf("PutRecord: %v", err)
		}
	}

	desc, err := m.DescribeStream(ctx, "s", "", 0, "")
	if err != nil {
		t.Fatalf("DescribeStream: %v", err)
	}

	shardID := desc.Shards[0].ShardID

	it, err := m.GetShardIterator(ctx, driver.GetShardIteratorInput{
		StreamName: "s", ShardID: shardID, ShardIteratorType: driver.IteratorTrimHorizon,
	})
	if err != nil {
		t.Fatalf("GetShardIterator: %v", err)
	}

	out, err := m.GetRecords(ctx, it, 2)
	if err != nil {
		t.Fatalf("GetRecords: %v", err)
	}

	if len(out.Records) != 2 {
		t.Fatalf("want 2 records, got %d", len(out.Records))
	}

	out2, err := m.GetRecords(ctx, out.NextShardIterator, 10)
	if err != nil {
		t.Fatalf("GetRecords(2): %v", err)
	}

	if len(out2.Records) != 1 {
		t.Fatalf("want 1 remaining record, got %d", len(out2.Records))
	}
}

func TestRetentionGuards(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	if err := m.CreateStream(ctx, driver.CreateStreamInput{StreamName: "s", ShardCount: 1}); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}

	// Default is 24h; decreasing below current fails, increasing works.
	if err := m.IncreaseStreamRetentionPeriod(ctx, "s", "", 48); err != nil {
		t.Fatalf("IncreaseStreamRetentionPeriod: %v", err)
	}

	if err := m.IncreaseStreamRetentionPeriod(ctx, "s", "", 24); err == nil {
		t.Fatal("increasing to a lower value should fail")
	}
}

func TestPutRecordRejectsOversizeData(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	if err := m.CreateStream(ctx, driver.CreateStreamInput{StreamName: "s", ShardCount: 1}); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}

	_, err := m.PutRecord(ctx, driver.PutRecordInput{
		StreamName:   "s",
		PartitionKey: "k",
		Data:         make([]byte, (1<<20)+1),
	})

	apiErr, ok := err.(*driver.APIError)
	if !ok || apiErr.Exception != driver.ExValidation {
		t.Fatalf("oversize record should be ValidationException, got %v", err)
	}
}

func TestPutRecordsRejectsTooManyRecords(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	if err := m.CreateStream(ctx, driver.CreateStreamInput{StreamName: "s", ShardCount: 1}); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}

	entries := make([]driver.PutRecordsRequestEntry, 501)
	for i := range entries {
		entries[i] = driver.PutRecordsRequestEntry{PartitionKey: "k", Data: []byte("x")}
	}

	_, _, err := m.PutRecords(ctx, "s", "", entries)

	apiErr, ok := err.(*driver.APIError)
	if !ok || apiErr.Exception != driver.ExValidation {
		t.Fatalf("501-record batch should be ValidationException, got %v", err)
	}
}

// newMockClock builds a Kinesis mock driven by a FakeClock so iterator expiry is
// deterministic.
func newMockClock(t *testing.T) (*kinesis.Mock, *config.FakeClock) {
	t.Helper()

	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	return kinesis.New(config.NewOptions(config.WithClock(clk))), clk
}

// seedShard creates a single-shard stream, writes n records, and returns the
// shard id.
func seedShard(t *testing.T, ctx context.Context, m *kinesis.Mock, n int) string {
	t.Helper()

	if err := m.CreateStream(ctx, driver.CreateStreamInput{StreamName: "s", ShardCount: 1}); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}

	for i := 0; i < n; i++ {
		if _, err := m.PutRecord(ctx, driver.PutRecordInput{StreamName: "s", PartitionKey: "k", Data: []byte{byte(i)}}); err != nil {
			t.Fatalf("PutRecord: %v", err)
		}
	}

	desc, err := m.DescribeStream(ctx, "s", "", 0, "")
	if err != nil {
		t.Fatalf("DescribeStream: %v", err)
	}

	return desc.Shards[0].ShardID
}

func trimHorizonIterator(t *testing.T, ctx context.Context, m *kinesis.Mock, shardID string) string {
	t.Helper()

	it, err := m.GetShardIterator(ctx, driver.GetShardIteratorInput{
		StreamName: "s", ShardID: shardID, ShardIteratorType: driver.IteratorTrimHorizon,
	})
	if err != nil {
		t.Fatalf("GetShardIterator: %v", err)
	}

	return it
}

func TestShardIteratorValidWithinTTL(t *testing.T) {
	ctx := context.Background()
	m, clk := newMockClock(t)

	shardID := seedShard(t, ctx, m, 3)
	it := trimHorizonIterator(t, ctx, m, shardID)

	// Just under the 5-minute window: the iterator still works.
	clk.Advance(5*time.Minute - time.Second)

	out, err := m.GetRecords(ctx, it, 10)
	if err != nil {
		t.Fatalf("GetRecords within TTL: %v", err)
	}

	if len(out.Records) != 3 {
		t.Fatalf("want 3 records, got %d", len(out.Records))
	}
}

func TestShardIteratorExpiresAfterTTL(t *testing.T) {
	ctx := context.Background()
	m, clk := newMockClock(t)

	shardID := seedShard(t, ctx, m, 1)
	it := trimHorizonIterator(t, ctx, m, shardID)

	// Past the 5-minute window: GetRecords must reject the iterator.
	clk.Advance(5*time.Minute + time.Second)

	_, err := m.GetRecords(ctx, it, 10)

	apiErr, ok := err.(*driver.APIError)
	if !ok || apiErr.Exception != driver.ExExpiredIterator {
		t.Fatalf("expired iterator should be ExpiredIteratorException, got %v", err)
	}
}

func TestGetRecordsRefreshesNextShardIterator(t *testing.T) {
	ctx := context.Background()
	m, clk := newMockClock(t)

	shardID := seedShard(t, ctx, m, 1)
	it := trimHorizonIterator(t, ctx, m, shardID)

	// A client polling in a loop: each GetRecords advances the clock by nearly
	// the full window, but the refreshed NextShardIterator keeps it alive.
	for i := 0; i < 5; i++ {
		out, err := m.GetRecords(ctx, it, 10)
		if err != nil {
			t.Fatalf("GetRecords poll %d: %v", i, err)
		}

		clk.Advance(5*time.Minute - time.Second)
		it = out.NextShardIterator
	}
}

func TestExpiredIteratorForMissingShard(t *testing.T) {
	ctx := context.Background()
	m, _ := newMockClock(t)

	seedShard(t, ctx, m, 1)

	// Split the single shard so a higher-indexed child shard (shardId-...002)
	// exists, and grab an iterator positioned on it.
	if err := m.SplitShard(ctx, "s", "", "shardId-000000000000",
		"170141183460469231731687303715884105728"); err != nil {
		t.Fatalf("SplitShard: %v", err)
	}

	it := trimHorizonIterator(t, ctx, m, "shardId-000000000002")

	// Recreate the stream with a single shard so the token's stream lookup
	// succeeds but its shard id no longer exists: the missing-shard
	// ExpiredIterator path must still fire.
	if err := m.DeleteStream(ctx, "s", "", true); err != nil {
		t.Fatalf("DeleteStream: %v", err)
	}

	if err := m.CreateStream(ctx, driver.CreateStreamInput{StreamName: "s", ShardCount: 1}); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}

	_, err := m.GetRecords(ctx, it, 10)

	apiErr, ok := err.(*driver.APIError)
	if !ok || apiErr.Exception != driver.ExExpiredIterator {
		t.Fatalf("missing shard should be ExpiredIteratorException, got %v", err)
	}
}
