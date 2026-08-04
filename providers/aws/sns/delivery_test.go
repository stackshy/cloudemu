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

// TestSNSToSQSDelivery is a regression guard for issue #319: publishing to an
// SNS topic with an SQS subscription must deliver the message to the queue
// (wrapped in the SNS notification envelope).
func TestSNSToSQSDelivery(t *testing.T) {
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

	if _, err := sns.Subscribe(ctx, sndriver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "sqs", Endpoint: q.ARN,
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if _, err := sns.Publish(ctx, sndriver.PublishInput{
		TopicID: topic.Name, Message: "hello", Subject: "hi",
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

	var envelope map[string]string
	if err := json.Unmarshal([]byte(msgs[0].Body), &envelope); err != nil {
		t.Fatalf("delivered body is not the SNS envelope JSON: %v (%s)", err, msgs[0].Body)
	}

	if envelope["Type"] != "Notification" || envelope["Message"] != "hello" || envelope["TopicArn"] != topic.ResourceID {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}
}
