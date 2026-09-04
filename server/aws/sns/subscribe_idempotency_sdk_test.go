package sns_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
)

// TestSDKSubscribeIdempotent guards real SNS's documented Subscribe semantics:
// a repeat Subscribe with the same (TopicArn, Protocol, Endpoint) returns the
// existing subscription's ARN instead of creating a duplicate.
func TestSDKSubscribeIdempotent(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	topic, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("idempotent-sub-topic")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	first, err := client.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn: topic.TopicArn,
		Protocol: aws.String("sqs"),
		Endpoint: aws.String("arn:aws:sqs:us-east-1:123456789012:my-queue"),
	})
	if err != nil {
		t.Fatalf("first Subscribe: %v", err)
	}

	second, err := client.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn: topic.TopicArn,
		Protocol: aws.String("sqs"),
		Endpoint: aws.String("arn:aws:sqs:us-east-1:123456789012:my-queue"),
	})
	if err != nil {
		t.Fatalf("second Subscribe: %v", err)
	}

	if aws.ToString(first.SubscriptionArn) != aws.ToString(second.SubscriptionArn) {
		t.Fatalf("Subscribe not idempotent: first ARN %q, second ARN %q",
			aws.ToString(first.SubscriptionArn), aws.ToString(second.SubscriptionArn))
	}

	subs, err := client.ListSubscriptionsByTopic(ctx, &awssns.ListSubscriptionsByTopicInput{TopicArn: topic.TopicArn})
	if err != nil {
		t.Fatalf("ListSubscriptionsByTopic: %v", err)
	}

	if len(subs.Subscriptions) != 1 {
		t.Fatalf("ListSubscriptionsByTopic returned %d subscriptions, want 1 (no duplicate)", len(subs.Subscriptions))
	}
}

// TestSDKSubscribeIdempotentDistinctEndpoint guards the negative case: a
// different endpoint on the same topic/protocol must still create a separate
// subscription rather than being folded into the idempotency match.
func TestSDKSubscribeIdempotentDistinctEndpoint(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	topic, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("distinct-sub-topic")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	if _, err := client.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn: topic.TopicArn,
		Protocol: aws.String("sqs"),
		Endpoint: aws.String("arn:aws:sqs:us-east-1:123456789012:queue-a"),
	}); err != nil {
		t.Fatalf("Subscribe queue-a: %v", err)
	}

	if _, err := client.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn: topic.TopicArn,
		Protocol: aws.String("sqs"),
		Endpoint: aws.String("arn:aws:sqs:us-east-1:123456789012:queue-b"),
	}); err != nil {
		t.Fatalf("Subscribe queue-b: %v", err)
	}

	subs, err := client.ListSubscriptionsByTopic(ctx, &awssns.ListSubscriptionsByTopicInput{TopicArn: topic.TopicArn})
	if err != nil {
		t.Fatalf("ListSubscriptionsByTopic: %v", err)
	}

	if len(subs.Subscriptions) != 2 {
		t.Fatalf("ListSubscriptionsByTopic returned %d subscriptions, want 2 (distinct endpoints)", len(subs.Subscriptions))
	}
}
