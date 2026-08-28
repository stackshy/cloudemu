package kinesis_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/kinesis"
	"github.com/stackshy/cloudemu/v2/services/kinesis/driver"
)

// recordingKinesisInvoker captures Kinesis -> Lambda event-source-mapping
// deliveries, mirroring providers/aws/dynamodb's recordingStreamInvoker. It
// always reports a mapping as present (delivered=true).
type recordingKinesisInvoker struct {
	arns     []string
	payloads [][]byte
}

func (r *recordingKinesisInvoker) DeliverEventSourceBatch(_ context.Context, arn string, payload []byte) (bool, error) {
	r.arns = append(r.arns, arn)
	r.payloads = append(r.payloads, payload)

	return true, nil
}

// TestPutRecordDeliversToLambda verifies that, once a Lambda invoker is wired,
// PutRecord delivers a real Kinesis Lambda event (see
// https://docs.aws.amazon.com/lambda/latest/dg/with-kinesis.html) tagged with
// the stream's ARN, with the record data base64-encoded.
func TestPutRecordDeliversToLambda(t *testing.T) {
	ctx := context.Background()
	m := kinesis.New(config.NewOptions(config.WithRegion("us-east-1"), config.WithAccountID("123456789012")))

	inv := &recordingKinesisInvoker{}
	m.SetLambdaInvoker(inv)

	if err := m.CreateStream(ctx, driver.CreateStreamInput{StreamName: "events", ShardCount: 1}); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}

	res, err := m.PutRecord(ctx, driver.PutRecordInput{
		StreamName: "events", PartitionKey: "pk-1", Data: []byte("hello-kinesis"),
	})
	if err != nil {
		t.Fatalf("PutRecord: %v", err)
	}

	wantARN := "arn:aws:kinesis:us-east-1:123456789012:stream/events"
	if len(inv.arns) != 1 || inv.arns[0] != wantARN {
		t.Fatalf("deliveries = %v, want exactly one to %s", inv.arns, wantARN)
	}

	var event struct {
		Records []struct {
			Kinesis struct {
				PartitionKey   string `json:"partitionKey"`
				SequenceNumber string `json:"sequenceNumber"`
				Data           string `json:"data"`
			} `json:"kinesis"`
			EventSource    string `json:"eventSource"`
			EventName      string `json:"eventName"`
			EventID        string `json:"eventID"`
			EventSourceARN string `json:"eventSourceARN"`
			AWSRegion      string `json:"awsRegion"`
		} `json:"Records"`
	}

	if err := json.Unmarshal(inv.payloads[0], &event); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}

	if len(event.Records) != 1 {
		t.Fatalf("Records = %d, want 1", len(event.Records))
	}

	rec := event.Records[0]

	if rec.EventSource != "aws:kinesis" || rec.EventName != "aws:kinesis:record" {
		t.Fatalf("eventSource=%q eventName=%q", rec.EventSource, rec.EventName)
	}

	if rec.EventSourceARN != wantARN || rec.AWSRegion != "us-east-1" {
		t.Fatalf("eventSourceARN=%q awsRegion=%q", rec.EventSourceARN, rec.AWSRegion)
	}

	if rec.Kinesis.PartitionKey != "pk-1" || rec.Kinesis.SequenceNumber != res.SequenceNumber {
		t.Fatalf("partitionKey=%q sequenceNumber=%q, want pk-1 / %s",
			rec.Kinesis.PartitionKey, rec.Kinesis.SequenceNumber, res.SequenceNumber)
	}

	decoded, err := base64.StdEncoding.DecodeString(rec.Kinesis.Data)
	if err != nil {
		t.Fatalf("data is not base64: %v", err)
	}

	if string(decoded) != "hello-kinesis" {
		t.Fatalf("data = %q, want hello-kinesis", decoded)
	}

	if rec.EventID != res.ShardID+":"+res.SequenceNumber {
		t.Fatalf("eventID = %q, want %s:%s", rec.EventID, res.ShardID, res.SequenceNumber)
	}
}

// TestPutRecordsDeliversBatchToLambda verifies PutRecords delivers a single
// event batch carrying every appended record to the mapped Lambda.
func TestPutRecordsDeliversBatchToLambda(t *testing.T) {
	ctx := context.Background()
	m := kinesis.New(config.NewOptions(config.WithRegion("us-east-1"), config.WithAccountID("123456789012")))

	inv := &recordingKinesisInvoker{}
	m.SetLambdaInvoker(inv)

	if err := m.CreateStream(ctx, driver.CreateStreamInput{StreamName: "batch", ShardCount: 1}); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}

	_, _, err := m.PutRecords(ctx, "batch", "", []driver.PutRecordsRequestEntry{
		{PartitionKey: "a", Data: []byte("1")},
		{PartitionKey: "b", Data: []byte("2")},
		{PartitionKey: "c", Data: []byte("3")},
	})
	if err != nil {
		t.Fatalf("PutRecords: %v", err)
	}

	if len(inv.payloads) != 1 {
		t.Fatalf("deliveries = %d, want 1 batch", len(inv.payloads))
	}

	var event struct {
		Records []json.RawMessage `json:"Records"`
	}

	if err := json.Unmarshal(inv.payloads[0], &event); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}

	if len(event.Records) != 3 {
		t.Fatalf("Records = %d, want 3", len(event.Records))
	}
}

// TestPutRecordNoInvokerNoDelivery verifies PutRecord never delivers when no
// Lambda invoker is wired (the default).
func TestPutRecordNoInvokerNoDelivery(t *testing.T) {
	ctx := context.Background()
	m := kinesis.New(config.NewOptions())

	if err := m.CreateStream(ctx, driver.CreateStreamInput{StreamName: "s", ShardCount: 1}); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}

	// No panic / no delivery path when the invoker is nil.
	if _, err := m.PutRecord(ctx, driver.PutRecordInput{StreamName: "s", PartitionKey: "k", Data: []byte("d")}); err != nil {
		t.Fatalf("PutRecord: %v", err)
	}
}
