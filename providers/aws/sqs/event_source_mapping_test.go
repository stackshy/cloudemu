package sqs

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

// recordingESMInvoker captures SQS -> Lambda event-source-mapping deliveries,
// mirroring providers/aws/dynamodb's recordingStreamInvoker. It always reports
// a mapping as present (delivered=true), since these tests are about handler
// success/failure, not mapping presence (see TestEventSourceInvokerNoMapping*
// for that). fail, when set, makes every delivery report a handler failure
// (as Lambda's DeliverEventSourceBatch does when the mapped function returns
// an error), so tests can drive the DLQ redrive path.
type recordingESMInvoker struct {
	arns     []string
	payloads [][]byte
	fail     bool
}

func (r *recordingESMInvoker) DeliverEventSourceBatch(_ context.Context, arn string, payload []byte) (bool, error) {
	r.arns = append(r.arns, arn)
	r.payloads = append(r.payloads, payload)

	if r.fail {
		return true, errors.New(errors.Internal, "handler failed")
	}

	return true, nil
}

// TestEventSourceInvokerDeliversOnSend verifies that, once an event source
// invoker is wired, SendMessage delivers a real SQS Lambda event (see
// https://docs.aws.amazon.com/lambda/latest/dg/with-sqs.html) tagged with the
// queue's ARN, and that a successfully processed message is deleted from the
// queue exactly as a real ESM would.
func TestEventSourceInvokerDeliversOnSend(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	inv := &recordingESMInvoker{}
	m.SetEventSourceInvoker(inv)

	q, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "esm-queue"})
	requireNoError(t, err)

	sendOut, err := m.SendMessage(ctx, driver.SendMessageInput{
		QueueURL: q.URL,
		Body:     "hello-lambda",
		MessageAttributes: map[string]driver.MessageAttributeValue{
			"myAttribute": {DataType: "String", StringValue: "myValue"},
		},
	})
	requireNoError(t, err)

	if len(inv.arns) != 1 || inv.arns[0] != q.ARN {
		t.Fatalf("deliveries = %v, want exactly one to %s", inv.arns, q.ARN)
	}

	var event struct {
		Records []struct {
			MessageID         string            `json:"messageId"`
			ReceiptHandle     string            `json:"receiptHandle"`
			Body              string            `json:"body"`
			Attributes        map[string]string `json:"attributes"`
			MD5OfBody         string            `json:"md5OfBody"`
			EventSource       string            `json:"eventSource"`
			EventSourceARN    string            `json:"eventSourceARN"`
			AWSRegion         string            `json:"awsRegion"`
			MessageAttributes map[string]struct {
				StringValue string `json:"stringValue"`
				DataType    string `json:"dataType"`
			} `json:"messageAttributes"`
		} `json:"Records"`
	}

	requireNoError(t, json.Unmarshal(inv.payloads[0], &event))

	if len(event.Records) != 1 {
		t.Fatalf("Records = %d, want 1", len(event.Records))
	}

	rec := event.Records[0]

	if rec.MessageID != sendOut.MessageID {
		t.Fatalf("messageId = %q, want %q", rec.MessageID, sendOut.MessageID)
	}

	if rec.ReceiptHandle == "" {
		t.Fatal("receiptHandle is empty")
	}

	if rec.Body != "hello-lambda" {
		t.Fatalf("body = %q, want %q", rec.Body, "hello-lambda")
	}

	if rec.EventSource != "aws:sqs" || rec.EventSourceARN != q.ARN || rec.AWSRegion != "us-east-1" {
		t.Fatalf("eventSource=%q eventSourceARN=%q awsRegion=%q", rec.EventSource, rec.EventSourceARN, rec.AWSRegion)
	}

	if rec.Attributes["ApproximateReceiveCount"] != "1" {
		t.Fatalf("ApproximateReceiveCount = %q, want 1", rec.Attributes["ApproximateReceiveCount"])
	}

	if rec.Attributes["SentTimestamp"] == "" || rec.Attributes["SenderId"] == "" ||
		rec.Attributes["ApproximateFirstReceiveTimestamp"] == "" {
		t.Fatalf("attributes missing standard fields: %+v", rec.Attributes)
	}

	if rec.MessageAttributes["myAttribute"].StringValue != "myValue" ||
		rec.MessageAttributes["myAttribute"].DataType != "String" {
		t.Fatalf("messageAttributes = %+v", rec.MessageAttributes)
	}

	// The message was processed successfully, so it must be gone from the queue.
	info, err := m.GetQueueInfo(ctx, q.URL)
	requireNoError(t, err)
	assertEqual(t, 0, info.ApproxMessageCount)
}

// TestEventSourceInvokerNoInvokerNoDelivery verifies SendMessage never delivers
// or blocks when no event source invoker is wired (the default).
func TestEventSourceInvokerNoInvokerNoDelivery(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	q, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "no-invoker-queue"})
	requireNoError(t, err)

	_, err = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "hi"})
	requireNoError(t, err)

	info, err := m.GetQueueInfo(ctx, q.URL)
	requireNoError(t, err)
	assertEqual(t, 1, info.ApproxMessageCount)
}

// noMappingESMInvoker always reports delivered=false, as Lambda's
// DeliverEventSourceBatch does when no enabled event-source-mapping targets
// the given ARN.
type noMappingESMInvoker struct{}

func (noMappingESMInvoker) DeliverEventSourceBatch(_ context.Context, _ string, _ []byte) (bool, error) {
	return false, nil
}

// TestEventSourceInvokerNoMappingLeavesMessageUntouched verifies that an
// invoker being wired is not, by itself, enough to consume a message: when no
// enabled event-source-mapping actually targets the queue's ARN
// (delivered=false), SendMessage must leave ordinary queue behavior
// completely unaffected, not silently delete every message it sends.
func TestEventSourceInvokerNoMappingLeavesMessageUntouched(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	m.SetEventSourceInvoker(noMappingESMInvoker{})

	q, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "unmapped-queue"})
	requireNoError(t, err)

	_, err = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "hi"})
	requireNoError(t, err)

	info, err := m.GetQueueInfo(ctx, q.URL)
	requireNoError(t, err)
	assertEqual(t, 1, info.ApproxMessageCount)

	msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: q.URL, MaxMessages: 1})
	requireNoError(t, err)

	if len(msgs) != 1 || msgs[0].SystemAttributes["ApproximateReceiveCount"] != "1" {
		t.Fatalf("messages = %+v, want one message with ApproximateReceiveCount=1", msgs)
	}
}

// TestEventSourceInvokerDLQRedrive verifies that a message whose mapped Lambda
// handler always fails is redriven to the dead-letter queue once its receive
// count exceeds RedrivePolicy's maxReceiveCount, exercising the same DLQ
// threshold ReceiveMessages honors (see exceedsMaxReceive / collectVisibleMessages)
// end to end through Lambda ESM delivery.
func TestEventSourceInvokerDLQRedrive(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	inv := &recordingESMInvoker{fail: true}
	m.SetEventSourceInvoker(inv)

	dlq, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "esm-dlq"})
	requireNoError(t, err)

	q, err := m.CreateQueue(ctx, driver.QueueConfig{
		Name: "esm-source",
		DeadLetterQueue: &driver.DeadLetterConfig{
			TargetQueueURL:  dlq.URL,
			MaxReceiveCount: 2,
		},
	})
	requireNoError(t, err)

	sendOut, err := m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "always-fails"})
	requireNoError(t, err)

	// The failing handler should have been invoked twice (MaxReceiveCount=2)
	// before the message was redriven to the DLQ.
	if len(inv.arns) != 2 {
		t.Fatalf("delivery attempts = %d, want 2", len(inv.arns))
	}

	srcInfo, err := m.GetQueueInfo(ctx, q.URL)
	requireNoError(t, err)
	assertEqual(t, 0, srcInfo.ApproxMessageCount)

	dlqMsgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: dlq.URL, MaxMessages: 10})
	requireNoError(t, err)

	if len(dlqMsgs) != 1 {
		t.Fatalf("DLQ messages = %d, want 1", len(dlqMsgs))
	}

	if dlqMsgs[0].MessageID != sendOut.MessageID || dlqMsgs[0].Body != "always-fails" {
		t.Fatalf("DLQ message = %+v, want id=%s body=always-fails", dlqMsgs[0], sendOut.MessageID)
	}
}

// TestEventSourceInvokerFailureNoDLQLeavesMessage verifies that, with no
// RedrivePolicy configured, a single failed delivery leaves the message in
// the source queue rather than retrying indefinitely or dropping it.
func TestEventSourceInvokerFailureNoDLQLeavesMessage(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	inv := &recordingESMInvoker{fail: true}
	m.SetEventSourceInvoker(inv)

	q, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "no-dlq-fail-queue"})
	requireNoError(t, err)

	_, err = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "will-fail"})
	requireNoError(t, err)

	if len(inv.arns) != 1 {
		t.Fatalf("delivery attempts = %d, want exactly 1 (no DLQ configured)", len(inv.arns))
	}

	qd, ok := m.queues.Get(q.URL)
	if !ok {
		t.Fatal("queue not found")
	}

	if len(qd.messages) != 1 {
		t.Fatalf("messages left in queue = %d, want 1", len(qd.messages))
	}
}
