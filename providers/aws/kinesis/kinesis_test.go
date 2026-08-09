package kinesis_test

import (
	"context"
	"testing"

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
