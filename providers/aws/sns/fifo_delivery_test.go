package sns_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	snsprovider "github.com/stackshy/cloudemu/v2/providers/aws/sns"
	sqsprovider "github.com/stackshy/cloudemu/v2/providers/aws/sqs"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
	sndriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
)

// TestPublishFIFOContentBasedDedupDeliversToSQS is a regression guard for the
// real-cloud-parity audit finding (scratchpad/pilot/SNS.report.md): publishing
// to a FIFO SNS topic with ContentBasedDeduplication=true and no explicit
// MessageDeduplicationId used to succeed (MessageId returned) while the message
// was silently dropped by the downstream FIFO SQS subscriber, because SNS never
// derived a dedup id before forwarding and the SQS mock's own FIFO validation
// rejected the send. SNS must derive the dedup id itself (SHA-256 hex of the
// message body, matching real AWS) so the message is actually delivered.
func TestPublishFIFOContentBasedDedupDeliversToSQS(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions()

	sqs := sqsprovider.New(opts)
	sns := snsprovider.New(opts)
	sns.SetSQSDeliverer(sqs)

	q, err := sqs.CreateQueue(ctx, mqdriver.QueueConfig{Name: "orders.fifo", FIFO: true})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	topic, err := sns.CreateTopic(ctx, sndriver.TopicConfig{
		Name: "orders.fifo", FifoTopic: true, ContentBasedDeduplication: true,
	})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	if _, err := sns.Subscribe(ctx, sndriver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "sqs", Endpoint: q.ARN,
		Attributes: map[string]string{"RawMessageDelivery": "true"},
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// No MessageDeduplicationId supplied: SNS must derive one from the body.
	const body = "order-123-created"

	if _, err := sns.Publish(ctx, sndriver.PublishInput{
		TopicID: topic.Name, Message: body, MessageGroupID: "orders",
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	msgs, err := sqs.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: q.URL, MaxMessages: 10})
	if err != nil {
		t.Fatalf("ReceiveMessages: %v", err)
	}

	if len(msgs) != 1 {
		t.Fatalf("expected the message to actually be delivered to the FIFO SQS subscriber, got %d messages", len(msgs))
	}

	if msgs[0].Body != body {
		t.Fatalf("unexpected body: %q", msgs[0].Body)
	}

	sum := sha256.Sum256([]byte(body))
	wantDedupID := hex.EncodeToString(sum[:])

	if got := msgs[0].SystemAttributes["MessageDeduplicationId"]; got != wantDedupID {
		t.Fatalf("expected derived dedup id %q, got %q", wantDedupID, got)
	}

	if got := msgs[0].SystemAttributes["MessageGroupId"]; got != "orders" {
		t.Fatalf("expected MessageGroupId %q, got %q", "orders", got)
	}
}

// TestPublishFIFOExplicitDedupIDHonored guards that an explicit
// MessageDeduplicationId on Publish is forwarded to the SQS subscriber as-is,
// even when the topic also has ContentBasedDeduplication enabled (the explicit
// id always wins, matching real SNS).
func TestPublishFIFOExplicitDedupIDHonored(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions()

	sqs := sqsprovider.New(opts)
	sns := snsprovider.New(opts)
	sns.SetSQSDeliverer(sqs)

	q, err := sqs.CreateQueue(ctx, mqdriver.QueueConfig{Name: "orders.fifo", FIFO: true})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	topic, err := sns.CreateTopic(ctx, sndriver.TopicConfig{
		Name: "orders.fifo", FifoTopic: true, ContentBasedDeduplication: true,
	})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	if _, err := sns.Subscribe(ctx, sndriver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "sqs", Endpoint: q.ARN,
		Attributes: map[string]string{"RawMessageDelivery": "true"},
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if _, err := sns.Publish(ctx, sndriver.PublishInput{
		TopicID: topic.Name, Message: "hello", MessageGroupID: "orders", MessageDeduplicationID: "explicit-id",
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	msgs, err := sqs.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: q.URL, MaxMessages: 10})
	if err != nil {
		t.Fatalf("ReceiveMessages: %v", err)
	}

	if len(msgs) != 1 {
		t.Fatalf("expected 1 delivered message, got %d", len(msgs))
	}

	if got := msgs[0].SystemAttributes["MessageDeduplicationId"]; got != "explicit-id" {
		t.Fatalf("expected explicit dedup id %q to be honored, got %q", "explicit-id", got)
	}
}

// TestPublishFIFOContentBasedDedupWindowStillEnforced guards that the derived
// dedup id still participates in the downstream SQS FIFO 5-minute
// deduplication window: two publishes of the same body within the window
// collapse to one delivered message, and a publish after the window elapses
// is delivered again.
func TestPublishFIFOContentBasedDedupWindowStillEnforced(t *testing.T) {
	ctx := context.Background()
	fc := config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc))

	sqs := sqsprovider.New(opts)
	sns := snsprovider.New(opts)
	sns.SetSQSDeliverer(sqs)

	q, err := sqs.CreateQueue(ctx, mqdriver.QueueConfig{Name: "orders.fifo", FIFO: true})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	topic, err := sns.CreateTopic(ctx, sndriver.TopicConfig{
		Name: "orders.fifo", FifoTopic: true, ContentBasedDeduplication: true,
	})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	if _, err := sns.Subscribe(ctx, sndriver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "sqs", Endpoint: q.ARN,
		Attributes: map[string]string{"RawMessageDelivery": "true"},
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	const body = "same-content"

	publish := func() {
		if _, err := sns.Publish(ctx, sndriver.PublishInput{
			TopicID: topic.Name, Message: body, MessageGroupID: "g",
		}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}

	publish()
	publish() // within the 5-minute window: same derived dedup id, must collapse

	msgs, err := sqs.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: q.URL, MaxMessages: 10})
	if err != nil {
		t.Fatalf("ReceiveMessages: %v", err)
	}

	if len(msgs) != 1 {
		t.Fatalf("expected the second identical publish to be deduplicated, got %d messages", len(msgs))
	}

	// Drain the queue so the next receive only reflects the post-window publish
	// (otherwise the first message would simply become visible again once its
	// visibility timeout elapses, which is unrelated to what this test guards).
	if err := sqs.DeleteMessage(ctx, q.URL, msgs[0].ReceiptHandle); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}

	fc.Advance(6 * time.Minute)
	publish() // window elapsed: delivered again

	msgs, err = sqs.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: q.URL, MaxMessages: 10})
	if err != nil {
		t.Fatalf("ReceiveMessages: %v", err)
	}

	if len(msgs) != 1 {
		t.Fatalf("expected the post-window publish to be delivered, got %d messages", len(msgs))
	}
}

// TestPublishFIFOContentBasedDedupEnvelope guards the non-raw (default)
// delivery path: the derived dedup id must reach the FIFO SQS queue even when
// the message is wrapped in the SNS notification envelope, not just under raw
// delivery.
func TestPublishFIFOContentBasedDedupEnvelope(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions()

	sqs := sqsprovider.New(opts)
	sns := snsprovider.New(opts)
	sns.SetSQSDeliverer(sqs)

	q, err := sqs.CreateQueue(ctx, mqdriver.QueueConfig{Name: "events.fifo", FIFO: true})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	topic, err := sns.CreateTopic(ctx, sndriver.TopicConfig{
		Name: "events.fifo", FifoTopic: true, ContentBasedDeduplication: true,
	})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	if _, err := sns.Subscribe(ctx, sndriver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "sqs", Endpoint: q.ARN,
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if _, err := sns.Publish(ctx, sndriver.PublishInput{
		TopicID: topic.Name, Message: "envelope-body", MessageGroupID: "g",
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	msgs, err := sqs.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: q.URL, MaxMessages: 10})
	if err != nil {
		t.Fatalf("ReceiveMessages: %v", err)
	}

	if len(msgs) != 1 {
		t.Fatalf("expected 1 delivered message, got %d", len(msgs))
	}

	var envelope map[string]any
	if err := json.Unmarshal([]byte(msgs[0].Body), &envelope); err != nil {
		t.Fatalf("delivered body is not the SNS envelope JSON: %v (%s)", err, msgs[0].Body)
	}

	if envelope["Message"] != "envelope-body" {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}

	if _, ok := msgs[0].SystemAttributes["MessageDeduplicationId"]; !ok {
		t.Fatalf("expected a derived dedup id on the enveloped delivery, got none: %+v", msgs[0].SystemAttributes)
	}
}
