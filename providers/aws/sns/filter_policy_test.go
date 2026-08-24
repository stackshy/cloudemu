package sns_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	snsprovider "github.com/stackshy/cloudemu/v2/providers/aws/sns"
	sqsprovider "github.com/stackshy/cloudemu/v2/providers/aws/sqs"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
	sndriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
)

// snsToSQS wires an SNS topic to an SQS queue, applies the given subscription
// attributes, publishes one message, and returns the delivered bodies.
func snsToSQS(t *testing.T, subAttrs map[string]string, msg string, attrs map[string]string) []string {
	t.Helper()

	ctx := context.Background()
	opts := config.NewOptions()

	sqs := sqsprovider.New(opts)
	sns := snsprovider.New(opts)
	sns.SetSQSDeliverer(sqs)

	q, err := sqs.CreateQueue(ctx, mqdriver.QueueConfig{Name: "inbox"})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	topic, err := sns.CreateTopic(ctx, sndriver.TopicConfig{Name: "feed"})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	sub, err := sns.Subscribe(ctx, sndriver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "sqs", Endpoint: q.ARN,
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	for k, v := range subAttrs {
		if err := sns.SetSubscriptionAttribute(ctx, sub.ID, k, v); err != nil {
			t.Fatalf("SetSubscriptionAttribute(%s): %v", k, err)
		}
	}

	if _, err := sns.Publish(ctx, sndriver.PublishInput{
		TopicID: topic.Name, Message: msg, Attributes: attrs,
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	msgs, err := sqs.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: q.URL, MaxMessages: 10})
	if err != nil {
		t.Fatalf("ReceiveMessages: %v", err)
	}

	bodies := make([]string, len(msgs))
	for i := range msgs {
		bodies[i] = msgs[i].Body
	}

	return bodies
}

func TestSNSFilterPolicyGatesDelivery(t *testing.T) {
	attrs := map[string]string{"FilterPolicy": `{"color":["red"]}`}

	if got := snsToSQS(t, attrs, "hi", map[string]string{"color": "red"}); len(got) != 1 {
		t.Fatalf("matching attribute: delivered %d, want 1", len(got))
	}

	if got := snsToSQS(t, attrs, "hi", map[string]string{"color": "blue"}); len(got) != 0 {
		t.Fatalf("non-matching attribute: delivered %d, want 0", len(got))
	}

	if got := snsToSQS(t, attrs, "hi", nil); len(got) != 0 {
		t.Fatalf("absent attribute: delivered %d, want 0", len(got))
	}
}

func TestSNSRawMessageDelivery(t *testing.T) {
	attrs := map[string]string{"RawMessageDelivery": "true"}

	got := snsToSQS(t, attrs, "hello-raw", map[string]string{"env": "prod"})
	if len(got) != 1 {
		t.Fatalf("delivered %d, want 1", len(got))
	}

	if got[0] != "hello-raw" {
		t.Fatalf("raw delivery body = %q, want %q", got[0], "hello-raw")
	}

	// A non-raw subscription still receives the Notification envelope.
	env := snsToSQS(t, nil, "hello-env", nil)
	if len(env) != 1 {
		t.Fatalf("envelope delivery count = %d, want 1", len(env))
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(env[0]), &parsed); err != nil || parsed["Type"] != "Notification" {
		t.Fatalf("non-raw body should be the Notification envelope: %s", env[0])
	}
}
