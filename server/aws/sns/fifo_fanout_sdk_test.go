package sns_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// fifoQueue creates a FIFO SQS queue (content-based dedup on) and returns its
// URL and ARN.
func fifoQueue(t *testing.T, sqs *awssqs.Client, name string) (url, arn string) {
	t.Helper()

	ctx := context.Background()

	q, err := sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{
		QueueName: aws.String(name),
		Attributes: map[string]string{
			"FifoQueue":                 "true",
			"ContentBasedDeduplication": "true",
		},
	})
	if err != nil {
		t.Fatalf("CreateQueue(fifo): %v", err)
	}

	attrs, err := sqs.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl:       q.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})
	if err != nil {
		t.Fatalf("GetQueueAttributes: %v", err)
	}

	return aws.ToString(q.QueueUrl), attrs.Attributes["QueueArn"]
}

// TestSDKSNSFifoTopicToFifoQueueDelivers reproduces the ordered-fanout pattern:
// an SNS FIFO topic subscribed by a FIFO SQS queue. Publishing with a
// MessageGroupId must deliver (was a silent zero-delivery loss when the wire
// dropped the group id), and the group id must be carried onto the SQS message.
func TestSDKSNSFifoTopicToFifoQueueDelivers(t *testing.T) {
	sns, sqs := newSNSAndSQS(t)
	ctx := context.Background()

	url, arn := fifoQueue(t, sqs, "ord-sink.fifo")

	topic, err := sns.CreateTopic(ctx, &awssns.CreateTopicInput{
		Name: aws.String("ord.fifo"),
		Attributes: map[string]string{
			"FifoTopic":                 "true",
			"ContentBasedDeduplication": "true",
		},
	})
	if err != nil {
		t.Fatalf("CreateTopic(fifo): %v", err)
	}

	sub, err := sns.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn:              topic.TopicArn,
		Protocol:              aws.String("sqs"),
		Endpoint:              aws.String(arn),
		ReturnSubscriptionArn: true,
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if _, err := sns.SetSubscriptionAttributes(ctx, &awssns.SetSubscriptionAttributesInput{
		SubscriptionArn: sub.SubscriptionArn,
		AttributeName:   aws.String("RawMessageDelivery"),
		AttributeValue:  aws.String("true"),
	}); err != nil {
		t.Fatalf("SetSubscriptionAttributes: %v", err)
	}

	const n = 5
	for i := range [n]struct{}{} {
		if _, err := sns.Publish(ctx, &awssns.PublishInput{
			TopicArn:       topic.TopicArn,
			Message:        aws.String(string(rune('a' + i))),
			MessageGroupId: aws.String("g1"),
		}); err != nil {
			t.Fatalf("Publish %d: %v", i, err)
		}
	}

	rcv, err := sqs.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl:            aws.String(url),
		MaxNumberOfMessages: 10,
		MessageSystemAttributeNames: []sqstypes.MessageSystemAttributeName{
			sqstypes.MessageSystemAttributeNameMessageGroupId,
		},
	})
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}

	if len(rcv.Messages) != n {
		t.Fatalf("FIFO fanout delivered %d messages, want %d (was 0 before fix)", len(rcv.Messages), n)
	}

	for i := range rcv.Messages {
		if got := rcv.Messages[i].Attributes["MessageGroupId"]; got != "g1" {
			t.Fatalf("message %d MessageGroupId = %q, want g1 (group id must carry through fanout)", i, got)
		}
	}
}

// TestSDKSNSNumericAttributeFilterPolicy reproduces a numeric filter policy on a
// message ATTRIBUTE: attribute values travel the wire as strings, so a numeric
// operator must still match one that encodes a number. A matching message was
// silently dropped before the fix.
func TestSDKSNSNumericAttributeFilterPolicy(t *testing.T) {
	sns, sqs := newSNSAndSQS(t)
	ctx := context.Background()

	url, arn := snsQueue(t, sqs, "priced")

	topic, err := sns.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("prices")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	sub, err := sns.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn:              topic.TopicArn,
		Protocol:              aws.String("sqs"),
		Endpoint:              aws.String(arn),
		ReturnSubscriptionArn: true,
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if _, err := sns.SetSubscriptionAttributes(ctx, &awssns.SetSubscriptionAttributesInput{
		SubscriptionArn: sub.SubscriptionArn,
		AttributeName:   aws.String("FilterPolicy"),
		AttributeValue:  aws.String(`{"price":[{"numeric":[">",100]}]}`),
	}); err != nil {
		t.Fatalf("SetSubscriptionAttributes: %v", err)
	}

	publish := func(price string) {
		if _, err := sns.Publish(ctx, &awssns.PublishInput{
			TopicArn: topic.TopicArn,
			Message:  aws.String("hi"),
			MessageAttributes: map[string]snstypes.MessageAttributeValue{
				"price": {DataType: aws.String("Number"), StringValue: aws.String(price)},
			},
		}); err != nil {
			t.Fatalf("Publish(price=%s): %v", price, err)
		}
	}

	publish("50")

	if got := drain(t, sqs, url); len(got) != 0 {
		t.Fatalf("price=50 delivered %d, want 0 (below threshold, filtered)", len(got))
	}

	publish("150")

	if got := drain(t, sqs, url); len(got) != 1 {
		t.Fatalf("price=150 delivered %d, want 1 (numeric attribute above threshold)", len(got))
	}
}

// TestSDKSNSMessageBodyNumericFilterUnchanged guards that the attribute-scope
// numeric coercion did not change body-scope semantics: a JSON number in the
// body still matches, and the policy still gates below-threshold messages.
func TestSDKSNSMessageBodyNumericFilterUnchanged(t *testing.T) {
	sns, sqs := newSNSAndSQS(t)
	ctx := context.Background()

	url, arn := snsQueue(t, sqs, "body-priced")

	topic, err := sns.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("body-prices")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	sub, err := sns.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn:              topic.TopicArn,
		Protocol:              aws.String("sqs"),
		Endpoint:              aws.String(arn),
		ReturnSubscriptionArn: true,
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	for name, value := range map[string]string{
		"FilterPolicyScope":  "MessageBody",
		"FilterPolicy":       `{"price":[{"numeric":[">",100]}]}`,
		"RawMessageDelivery": "true",
	} {
		if _, err := sns.SetSubscriptionAttributes(ctx, &awssns.SetSubscriptionAttributesInput{
			SubscriptionArn: sub.SubscriptionArn,
			AttributeName:   aws.String(name),
			AttributeValue:  aws.String(value),
		}); err != nil {
			t.Fatalf("SetSubscriptionAttributes(%s): %v", name, err)
		}
	}

	if _, err := sns.Publish(ctx, &awssns.PublishInput{
		TopicArn: topic.TopicArn,
		Message:  aws.String(`{"price":50}`),
	}); err != nil {
		t.Fatalf("Publish(50): %v", err)
	}

	if got := drain(t, sqs, url); len(got) != 0 {
		t.Fatalf("body price=50 delivered %d, want 0", len(got))
	}

	if _, err := sns.Publish(ctx, &awssns.PublishInput{
		TopicArn: topic.TopicArn,
		Message:  aws.String(`{"price":150}`),
	}); err != nil {
		t.Fatalf("Publish(150): %v", err)
	}

	if got := drain(t, sqs, url); len(got) != 1 {
		t.Fatalf("body price=150 delivered %d, want 1", len(got))
	}
}
