package kinesis_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/kinesis"
	"github.com/stackshy/cloudemu/v2/services/kinesis/driver"
)

func wantException(t *testing.T, err error, exception string) {
	t.Helper()

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != exception {
		t.Fatalf("err = %v, want %s", err, exception)
	}
}

func TestPutRecordRejectsPartitionKeyOver256Chars(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	if err := m.CreateStream(ctx, driver.CreateStreamInput{StreamName: "s", ShardCount: 1}); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}

	// 256 characters is the documented maximum and is accepted, counting
	// Unicode characters (each "é" is 2 bytes) rather than bytes.
	for _, ok := range []string{strings.Repeat("k", 256), strings.Repeat("é", 256)} {
		if _, err := m.PutRecord(ctx, driver.PutRecordInput{StreamName: "s", PartitionKey: ok, Data: []byte("d")}); err != nil {
			t.Fatalf("PutRecord with %d-char key: %v", len([]rune(ok)), err)
		}
	}

	_, err := m.PutRecord(ctx, driver.PutRecordInput{
		StreamName: "s", PartitionKey: strings.Repeat("k", 257), Data: []byte("d"),
	})
	wantException(t, err, driver.ExValidation)

	if !strings.Contains(err.Error(), "at 'partitionKey' failed to satisfy constraint: "+
		"Member must have length less than or equal to 256") {
		t.Fatalf("message = %q", err.Error())
	}
}

func TestPutRecordsRejectsPartitionKeyOver256Chars(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	shardID := seedShard(t, ctx, m, 0)

	_, _, err := m.PutRecords(ctx, "s", "", []driver.PutRecordsRequestEntry{
		{PartitionKey: "ok", Data: []byte("a")},
		{PartitionKey: strings.Repeat("k", 257), Data: []byte("b")},
	})
	wantException(t, err, driver.ExValidation)

	if !strings.Contains(err.Error(), "at 'records.2.member.partitionKey'") {
		t.Fatalf("message = %q, want the offending entry's member path", err.Error())
	}

	// The whole request is rejected: not even the valid entry is written.
	out, err := m.GetRecords(ctx, trimHorizonIterator(t, ctx, m, shardID), 0)
	if err != nil {
		t.Fatalf("GetRecords: %v", err)
	}

	if len(out.Records) != 0 {
		t.Fatalf("records written = %d, want 0", len(out.Records))
	}
}

func TestGetRecordsRejectsOutOfRangeLimit(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	shardID := seedShard(t, ctx, m, 3)
	it := trimHorizonIterator(t, ctx, m, shardID)

	for _, bad := range []int32{-1, 10001} {
		_, err := m.GetRecords(ctx, it, bad)
		wantException(t, err, driver.ExInvalidArgument)
	}

	// The bounds themselves are valid; 0 is the Go zero value for "omitted".
	for _, ok := range []int32{0, 1, 10000} {
		if _, err := m.GetRecords(ctx, it, ok); err != nil {
			t.Fatalf("GetRecords(limit=%d): %v", ok, err)
		}
	}
}

func newSettleMock(t *testing.T) (*kinesis.Mock, *config.FakeClock) {
	t.Helper()

	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	return kinesis.New(config.NewOptions(config.WithClock(clk), config.WithAsyncSettle())), clk
}

func streamStatus(t *testing.T, m *kinesis.Mock, name string) string {
	t.Helper()

	sum, err := m.DescribeStreamSummary(context.Background(), name, "")
	if err != nil {
		t.Fatalf("DescribeStreamSummary: %v", err)
	}

	desc, err := m.DescribeStream(context.Background(), name, "", 0, "")
	if err != nil {
		t.Fatalf("DescribeStream: %v", err)
	}

	if desc.StreamStatus != sum.StreamStatus {
		t.Fatalf("DescribeStream status %s != DescribeStreamSummary status %s", desc.StreamStatus, sum.StreamStatus)
	}

	return sum.StreamStatus
}

func TestCreateStreamReportsCreatingUnderAsyncSettle(t *testing.T) {
	ctx := context.Background()
	m, clk := newSettleMock(t)

	if err := m.CreateStream(ctx, driver.CreateStreamInput{StreamName: "s", ShardCount: 1}); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}

	if got := streamStatus(t, m, "s"); got != driver.StatusCreating {
		t.Fatalf("status right after create = %s, want CREATING", got)
	}

	list, err := m.ListStreams(ctx, "", "", 0)
	if err != nil || len(list.StreamSummaries) != 1 || list.StreamSummaries[0].StreamStatus != driver.StatusCreating {
		t.Fatalf("ListStreams = %+v, %v; want one CREATING summary", list, err)
	}

	// Only an ACTIVE stream can be deleted.
	err = m.DeleteStream(ctx, "s", "", false)
	if !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("DeleteStream while CREATING = %v, want ResourceInUse", err)
	}

	clk.Advance(2 * time.Second)

	if got := streamStatus(t, m, "s"); got != driver.StatusActive {
		t.Fatalf("status after settle = %s, want ACTIVE", got)
	}
}

func TestDeleteStreamReportsDeletingUnderAsyncSettle(t *testing.T) {
	ctx := context.Background()
	m, clk := newSettleMock(t)

	if err := m.CreateStream(ctx, driver.CreateStreamInput{StreamName: "s", ShardCount: 1}); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}

	clk.Advance(2 * time.Second)

	if err := m.DeleteStream(ctx, "s", "", false); err != nil {
		t.Fatalf("DeleteStream: %v", err)
	}

	if got := streamStatus(t, m, "s"); got != driver.StatusDeleting {
		t.Fatalf("status right after delete = %s, want DELETING", got)
	}

	// Kinesis may keep serving data operations on a DELETING stream.
	if _, err := m.PutRecord(ctx, driver.PutRecordInput{StreamName: "s", PartitionKey: "k", Data: []byte("d")}); err != nil {
		t.Fatalf("PutRecord while DELETING: %v", err)
	}

	if err := m.DeleteStream(ctx, "s", "", false); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("second DeleteStream = %v, want ResourceInUse", err)
	}

	if err := m.CreateStream(ctx, driver.CreateStreamInput{StreamName: "s", ShardCount: 1}); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("re-create while DELETING = %v, want ResourceInUse", err)
	}

	// A snapshot taken mid-deletion must not resurrect the stream.
	raw, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	if strings.Contains(string(raw), `"StreamName":"s"`) {
		t.Fatalf("snapshot still carries the DELETING stream: %s", raw)
	}

	clk.Advance(2 * time.Second)

	if _, err := m.DescribeStreamSummary(ctx, "s", ""); !cerrors.IsNotFound(err) {
		t.Fatalf("DescribeStreamSummary after delete settles = %v, want ResourceNotFound", err)
	}

	list, err := m.ListStreams(ctx, "", "", 0)
	if err != nil || len(list.StreamNames) != 0 {
		t.Fatalf("ListStreams after delete = %+v, %v; want empty", list, err)
	}

	// The name is free again once deletion has completed.
	if err := m.CreateStream(ctx, driver.CreateStreamInput{StreamName: "s", ShardCount: 1}); err != nil {
		t.Fatalf("re-create after delete: %v", err)
	}
}

func TestStreamLifecycleImmediateWithoutAsyncSettle(t *testing.T) {
	ctx := context.Background()
	m, _ := newMockClock(t)

	if err := m.CreateStream(ctx, driver.CreateStreamInput{StreamName: "s", ShardCount: 1}); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}

	if got := streamStatus(t, m, "s"); got != driver.StatusActive {
		t.Fatalf("status = %s, want ACTIVE immediately (AsyncSettle off)", got)
	}

	if err := m.DeleteStream(ctx, "s", "", false); err != nil {
		t.Fatalf("DeleteStream: %v", err)
	}

	if _, err := m.DescribeStreamSummary(ctx, "s", ""); !cerrors.IsNotFound(err) {
		t.Fatalf("DescribeStreamSummary after delete = %v, want ResourceNotFound immediately", err)
	}
}

// subscribeFixture registers a consumer on stream "s" and returns its ARN.
func subscribeFixture(t *testing.T, ctx context.Context, m *kinesis.Mock) string {
	t.Helper()

	sum, err := m.DescribeStreamSummary(ctx, "s", "")
	if err != nil {
		t.Fatalf("DescribeStreamSummary: %v", err)
	}

	c, err := m.RegisterStreamConsumer(ctx, sum.StreamARN, "efo")
	if err != nil {
		t.Fatalf("RegisterStreamConsumer: %v", err)
	}

	return c.ConsumerARN
}

// TestSubscribeToShardContinuationResumesWithoutReplay: the continuation of an
// event that delivered nothing must still capture the read position, so a
// LATEST subscription resumed with AFTER_SEQUENCE_NUMBER sees only new records.
func TestSubscribeToShardContinuationResumesWithoutReplay(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	shardID := seedShard(t, ctx, m, 2)
	consumer := subscribeFixture(t, ctx, m)

	first, err := m.SubscribeToShard(ctx, driver.SubscribeToShardInput{
		ConsumerARN: consumer, ShardID: shardID, StartingPositionType: driver.IteratorLatest,
	})
	if err != nil {
		t.Fatalf("SubscribeToShard LATEST: %v", err)
	}

	if len(first.Records) != 0 || first.ContinuationSequenceNumber == "" {
		t.Fatalf("LATEST event = %+v, want no records and a continuation", first)
	}

	if _, err := m.PutRecord(ctx, driver.PutRecordInput{StreamName: "s", PartitionKey: "k", Data: []byte("new")}); err != nil {
		t.Fatalf("PutRecord: %v", err)
	}

	next, err := m.SubscribeToShard(ctx, driver.SubscribeToShardInput{
		ConsumerARN: consumer, ShardID: shardID,
		StartingPositionType:   driver.IteratorAfterSequenceNumber,
		StartingSequenceNumber: first.ContinuationSequenceNumber,
	})
	if err != nil {
		t.Fatalf("SubscribeToShard AFTER continuation: %v", err)
	}

	if len(next.Records) != 1 || string(next.Records[0].Data) != "new" {
		t.Fatalf("resumed records = %+v, want only the new record", next.Records)
	}
}

// TestSubscribeToShardContinuationOnEmptyShard: on a shard with no records the
// continuation is the shard's starting sequence number, which must be accepted
// as AFTER_SEQUENCE_NUMBER (it precedes every record).
func TestSubscribeToShardContinuationOnEmptyShard(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	shardID := seedShard(t, ctx, m, 0)
	consumer := subscribeFixture(t, ctx, m)

	first, err := m.SubscribeToShard(ctx, driver.SubscribeToShardInput{
		ConsumerARN: consumer, ShardID: shardID, StartingPositionType: driver.IteratorTrimHorizon,
	})
	if err != nil {
		t.Fatalf("SubscribeToShard: %v", err)
	}

	if _, err := m.PutRecord(ctx, driver.PutRecordInput{StreamName: "s", PartitionKey: "k", Data: []byte("x")}); err != nil {
		t.Fatalf("PutRecord: %v", err)
	}

	next, err := m.SubscribeToShard(ctx, driver.SubscribeToShardInput{
		ConsumerARN: consumer, ShardID: shardID,
		StartingPositionType:   driver.IteratorAfterSequenceNumber,
		StartingSequenceNumber: first.ContinuationSequenceNumber,
	})
	if err != nil {
		t.Fatalf("SubscribeToShard AFTER shard start: %v", err)
	}

	if len(next.Records) != 1 {
		t.Fatalf("records = %d, want 1", len(next.Records))
	}
}

// TestSubscribeToShardSignalsShardEnd: reading a closed shard to its end
// returns ChildShards and no continuation, the shard-end signal.
func TestSubscribeToShardSignalsShardEnd(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	shardID := seedShard(t, ctx, m, 1)
	consumer := subscribeFixture(t, ctx, m)

	if _, _, err := m.UpdateShardCount(ctx, "s", "", 2, "UNIFORM_SCALING"); err != nil {
		t.Fatalf("UpdateShardCount: %v", err)
	}

	res, err := m.SubscribeToShard(ctx, driver.SubscribeToShardInput{
		ConsumerARN: consumer, ShardID: shardID, StartingPositionType: driver.IteratorTrimHorizon,
	})
	if err != nil {
		t.Fatalf("SubscribeToShard: %v", err)
	}

	if len(res.Records) != 1 || res.ContinuationSequenceNumber != "" || len(res.ChildShards) != 2 {
		t.Fatalf("closed-shard event = %+v, want 1 record, empty continuation, 2 child shards", res)
	}
}
