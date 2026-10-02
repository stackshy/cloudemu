package kinesis_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	"github.com/stackshy/cloudemu/v2/providers/aws/kinesis"
	"github.com/stackshy/cloudemu/v2/services/kinesis/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// TestStreamLevelMetrics pins the stream-level AWS/Kinesis metrics real Kinesis
// publishes for PutRecord, PutRecords and GetRecords, on the StreamName
// dimension with their documented units.
func TestStreamLevelMetrics(t *testing.T) {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk))
	m := kinesis.New(opts)
	cw := cloudwatch.New(opts)
	m.SetMonitoring(cw)

	ctx := context.Background()

	if err := m.CreateStream(ctx, driver.CreateStreamInput{StreamName: "s", ShardCount: 1}); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}

	if _, err := m.PutRecord(ctx, driver.PutRecordInput{StreamName: "s", PartitionKey: "k", Data: []byte("hello")}); err != nil {
		t.Fatalf("PutRecord: %v", err)
	}

	// One accepted record (4 bytes) and one rejected entry (missing key).
	if _, _, err := m.PutRecords(ctx, "s", "", []driver.PutRecordsRequestEntry{
		{PartitionKey: "k", Data: []byte("abcd")},
		{Data: []byte("zz")},
	}); err != nil {
		t.Fatalf("PutRecords: %v", err)
	}

	shards, err := m.ListShards(ctx, driver.ListShardsInput{StreamName: "s"})
	if err != nil {
		t.Fatalf("ListShards: %v", err)
	}

	it, err := m.GetShardIterator(ctx, driver.GetShardIteratorInput{
		StreamName: "s", ShardID: shards.Shards[0].ShardID, ShardIteratorType: "TRIM_HORIZON",
	})
	if err != nil {
		t.Fatalf("GetShardIterator: %v", err)
	}

	if _, err = m.GetRecords(ctx, it, 0); err != nil {
		t.Fatalf("GetRecords: %v", err)
	}

	want := []struct {
		name string
		sum  float64
		unit string
	}{
		{"IncomingRecords", 2, "Count"},
		{"IncomingBytes", 9, "Bytes"},
		{"PutRecord.Success", 1, "Count"},
		{"PutRecord.Bytes", 5, "Bytes"},
		{"PutRecords.TotalRecords", 2, "Count"},
		{"PutRecords.SuccessfulRecords", 1, "Count"},
		{"PutRecords.FailedRecords", 1, "Count"},
		{"PutRecords.Success", 1, "Count"},
		{"GetRecords.Records", 2, "Count"},
		{"GetRecords.Bytes", 9, "Bytes"},
		{"GetRecords.Success", 1, "Count"},
		{"GetRecords.IteratorAgeMilliseconds", 0, "Milliseconds"},
	}

	for _, w := range want {
		res, gerr := cw.GetMetricData(ctx, mondriver.GetMetricInput{
			Namespace: "AWS/Kinesis", MetricName: w.name,
			Dimensions: map[string]string{"StreamName": "s"},
			StartTime:  clk.Now().Add(-time.Minute), EndTime: clk.Now().Add(time.Minute),
			Period: 60, Stat: "Sum",
		})
		if gerr != nil {
			t.Fatalf("GetMetricData %s: %v", w.name, gerr)
		}

		if len(res.Values) != 1 || res.Values[0] != w.sum || res.Unit != w.unit {
			t.Errorf("%s = %v unit %q, want [%v] unit %q", w.name, res.Values, res.Unit, w.sum, w.unit)
		}
	}
}
