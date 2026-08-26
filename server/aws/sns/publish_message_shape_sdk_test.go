package sns_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
)

// subscribeSQS creates a topic, subscribes an SQS queue to it, and returns the
// topic ARN and the queue URL. Delivery uses the default (non-raw) envelope so
// tests can inspect the SNS Notification JSON.
func subscribeSQS(t *testing.T, sns *awssns.Client, sqs *awssqs.Client, topicName, queueName string) (topicARN, queueURL string) {
	t.Helper()

	ctx := context.Background()

	url, arn := snsQueue(t, sqs, queueName)

	topic, err := sns.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String(topicName)})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	if _, err := sns.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn:              topic.TopicArn,
		Protocol:              aws.String("sqs"),
		Endpoint:              aws.String(arn),
		ReturnSubscriptionArn: true,
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	return aws.ToString(topic.TopicArn), url
}

// TestSDKSNSPublishMessageStructureJSON asserts a MessageStructure=json publish
// resolves the per-protocol value: an SQS subscriber must receive the "sqs"
// entry, not the raw {"default":...,"sqs":...} blob.
func TestSDKSNSPublishMessageStructureJSON(t *testing.T) {
	sns, sqs := newSNSAndSQS(t)
	ctx := context.Background()

	topicARN, url := subscribeSQS(t, sns, sqs, "structured", "structured-sink")

	if _, err := sns.Publish(ctx, &awssns.PublishInput{
		TopicArn:         aws.String(topicARN),
		Message:          aws.String(`{"default":"d","sqs":"s"}`),
		MessageStructure: aws.String("json"),
	}); err != nil {
		t.Fatalf("Publish(json): %v", err)
	}

	bodies := drain(t, sqs, url)
	if len(bodies) != 1 {
		t.Fatalf("delivered %d messages, want 1", len(bodies))
	}

	var env map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &env); err != nil {
		t.Fatalf("envelope not JSON: %v (%s)", err, bodies[0])
	}

	if got := env["Message"]; got != "s" {
		t.Fatalf("Message = %v, want %q (the per-protocol sqs value)", got, "s")
	}
}

// TestSDKSNSPublishMessageStructureFallbackDefault asserts a protocol without a
// specific key falls back to "default", and that a missing default is rejected.
func TestSDKSNSPublishMessageStructureFallbackDefault(t *testing.T) {
	sns, sqs := newSNSAndSQS(t)
	ctx := context.Background()

	topicARN, url := subscribeSQS(t, sns, sqs, "fallback", "fallback-sink")

	if _, err := sns.Publish(ctx, &awssns.PublishInput{
		TopicArn:         aws.String(topicARN),
		Message:          aws.String(`{"default":"only-default"}`),
		MessageStructure: aws.String("json"),
	}); err != nil {
		t.Fatalf("Publish(default-only): %v", err)
	}

	bodies := drain(t, sqs, url)
	if len(bodies) != 1 {
		t.Fatalf("delivered %d messages, want 1", len(bodies))
	}

	var env map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &env); err != nil {
		t.Fatalf("envelope not JSON: %v", err)
	}

	if got := env["Message"]; got != "only-default" {
		t.Fatalf("Message = %v, want fallback to default", got)
	}

	if _, err := sns.Publish(ctx, &awssns.PublishInput{
		TopicArn:         aws.String(topicARN),
		Message:          aws.String(`{"sqs":"s"}`),
		MessageStructure: aws.String("json"),
	}); err == nil {
		t.Fatal("Publish with no default entry succeeded, want InvalidParameter")
	}

	if _, err := sns.Publish(ctx, &awssns.PublishInput{
		TopicArn:         aws.String(topicARN),
		Message:          aws.String("not json"),
		MessageStructure: aws.String("json"),
	}); err == nil {
		t.Fatal("Publish of non-JSON body with json structure succeeded, want InvalidParameter")
	}
}

// TestSDKSNSPublishNumberAttributeType asserts a Number message attribute keeps
// its DataType end-to-end: the SNS->SQS envelope must show "Type":"Number", not
// a flattened "String".
func TestSDKSNSPublishNumberAttributeType(t *testing.T) {
	sns, sqs := newSNSAndSQS(t)
	ctx := context.Background()

	topicARN, url := subscribeSQS(t, sns, sqs, "typed", "typed-sink")

	if _, err := sns.Publish(ctx, &awssns.PublishInput{
		TopicArn: aws.String(topicARN),
		Message:  aws.String("hi"),
		MessageAttributes: map[string]snstypes.MessageAttributeValue{
			"price": {DataType: aws.String("Number"), StringValue: aws.String("42")},
		},
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	bodies := drain(t, sqs, url)
	if len(bodies) != 1 {
		t.Fatalf("delivered %d messages, want 1", len(bodies))
	}

	var env struct {
		MessageAttributes map[string]struct {
			Type  string `json:"Type"`
			Value string `json:"Value"`
		} `json:"MessageAttributes"`
	}
	if err := json.Unmarshal([]byte(bodies[0]), &env); err != nil {
		t.Fatalf("envelope not JSON: %v", err)
	}

	price, ok := env.MessageAttributes["price"]
	if !ok {
		t.Fatalf("price attribute missing from envelope: %s", bodies[0])
	}

	if price.Type != "Number" {
		t.Fatalf("price Type = %q, want Number", price.Type)
	}

	if price.Value != "42" {
		t.Fatalf("price Value = %q, want 42", price.Value)
	}
}
