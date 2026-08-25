package dynamodb

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/database/driver"
)

// recordingStreamInvoker captures DynamoDB-stream -> Lambda deliveries.
type recordingStreamInvoker struct {
	arns     []string
	payloads [][]byte
}

func (r *recordingStreamInvoker) DeliverEventSourceBatch(_ context.Context, arn string, payload []byte) (bool, error) {
	r.arns = append(r.arns, arn)
	r.payloads = append(r.payloads, payload)

	return true, nil
}

// TestStreamInvokerDeliversOnWrite verifies that, once a stream invoker is wired,
// item writes to a stream-enabled table deliver a DynamoDB Streams event batch
// tagged with the table's stream ARN, and that a table without a stream never
// delivers.
func TestStreamInvokerDeliversOnWrite(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	inv := &recordingStreamInvoker{}
	m.SetStreamInvoker(inv)

	if err := m.CreateTable(ctx, driver.TableConfig{
		Name: "streamed", PartitionKey: "Id",
		StreamEnabled: true, StreamViewType: ViewNewAndOld,
	}); err != nil {
		t.Fatalf("CreateTable(streamed): %v", err)
	}

	if err := m.CreateTable(ctx, driver.TableConfig{Name: "plain", PartitionKey: "Id"}); err != nil {
		t.Fatalf("CreateTable(plain): %v", err)
	}

	streamARN := m.tables["streamed"].config.StreamArn
	if streamARN == "" {
		t.Fatal("stream table has no StreamArn")
	}

	// A write to the plain table must not deliver anything.
	if err := m.PutItem(ctx, "plain", map[string]any{"Id": "p1"}); err != nil {
		t.Fatalf("PutItem(plain): %v", err)
	}

	if len(inv.arns) != 0 {
		t.Fatalf("plain-table write delivered %v, want none", inv.arns)
	}

	// A write to the streamed table delivers exactly one batch to the stream ARN.
	if err := m.PutItem(ctx, "streamed", map[string]any{"Id": "s1", "Msg": "hi"}); err != nil {
		t.Fatalf("PutItem(streamed): %v", err)
	}

	if len(inv.arns) != 1 || inv.arns[0] != streamARN {
		t.Fatalf("deliveries = %v, want one to %s", inv.arns, streamARN)
	}

	var event struct {
		Records []struct {
			EventName      string `json:"eventName"`
			EventSourceARN string `json:"eventSourceARN"`
			DynamoDB       struct {
				NewImage map[string]map[string]any `json:"NewImage"`
			} `json:"dynamodb"`
		} `json:"Records"`
	}

	if err := json.Unmarshal(inv.payloads[0], &event); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if len(event.Records) != 1 {
		t.Fatalf("Records = %d, want 1", len(event.Records))
	}

	rec := event.Records[0]
	if rec.EventName != "INSERT" || rec.EventSourceARN != streamARN {
		t.Fatalf("record eventName=%q eventSourceARN=%q", rec.EventName, rec.EventSourceARN)
	}

	if rec.DynamoDB.NewImage["Msg"]["S"] != "hi" {
		t.Fatalf("NewImage.Msg.S = %v, want hi", rec.DynamoDB.NewImage["Msg"]["S"])
	}
}

// TestTransactWriteDeliversBatch verifies a multi-item transact write delivers
// the records of one stream as a single batched event.
func TestTransactWriteDeliversBatch(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	inv := &recordingStreamInvoker{}
	m.SetStreamInvoker(inv)

	if err := m.CreateTable(ctx, driver.TableConfig{
		Name: "t", PartitionKey: "Id", StreamEnabled: true, StreamViewType: ViewNewImage,
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	if err := m.TransactWriteItems(ctx, "t",
		[]map[string]any{{"Id": "a"}, {"Id": "b"}}, nil); err != nil {
		t.Fatalf("TransactWriteItems: %v", err)
	}

	if len(inv.payloads) != 1 {
		t.Fatalf("deliveries = %d, want 1 batched delivery", len(inv.payloads))
	}

	var event struct {
		Records []json.RawMessage `json:"Records"`
	}

	if err := json.Unmarshal(inv.payloads[0], &event); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if len(event.Records) != 2 {
		t.Fatalf("batched Records = %d, want 2", len(event.Records))
	}
}
