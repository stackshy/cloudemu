package sns_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
)

// TestSDKSetTopicAttributesSignatureVersionAndTracingConfig guards a
// regression where SetTopicAttributes silently dropped SignatureVersion and
// TracingConfig (no case in the attribute switch), causing a perpetual
// Terraform diff on aws_sns_topic's signature_version / tracing_config.
func TestSDKSetTopicAttributesSignatureVersionAndTracingConfig(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	topic, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("sig-tracing")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	if _, err := client.SetTopicAttributes(ctx, &awssns.SetTopicAttributesInput{
		TopicArn:       topic.TopicArn,
		AttributeName:  aws.String("SignatureVersion"),
		AttributeValue: aws.String("2"),
	}); err != nil {
		t.Fatalf("SetTopicAttributes(SignatureVersion): %v", err)
	}

	if _, err := client.SetTopicAttributes(ctx, &awssns.SetTopicAttributesInput{
		TopicArn:       topic.TopicArn,
		AttributeName:  aws.String("TracingConfig"),
		AttributeValue: aws.String("Active"),
	}); err != nil {
		t.Fatalf("SetTopicAttributes(TracingConfig): %v", err)
	}

	attrs := topicAttributes(t, client, aws.ToString(topic.TopicArn))

	if attrs["SignatureVersion"] != "2" {
		t.Fatalf("SignatureVersion = %q, want 2", attrs["SignatureVersion"])
	}

	if attrs["TracingConfig"] != "Active" {
		t.Fatalf("TracingConfig = %q, want Active", attrs["TracingConfig"])
	}
}

// TestSDKSetTopicAttributesSQSFeedbackFamily guards the delivery-status
// feedback attributes Terraform's aws_sns_topic exposes as
// sqs_success_feedback_role_arn / sqs_success_feedback_sample_rate /
// sqs_failure_feedback_role_arn — previously dropped silently.
func TestSDKSetTopicAttributesSQSFeedbackFamily(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	topic, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("sqs-feedback")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	sets := map[string]string{
		"SQSSuccessFeedbackRoleArn":    "arn:aws:iam::123456789012:role/sns-feedback",
		"SQSSuccessFeedbackSampleRate": "100",
		"SQSFailureFeedbackRoleArn":    "arn:aws:iam::123456789012:role/sns-feedback",
	}

	for name, value := range sets {
		if _, err := client.SetTopicAttributes(ctx, &awssns.SetTopicAttributesInput{
			TopicArn:       topic.TopicArn,
			AttributeName:  aws.String(name),
			AttributeValue: aws.String(value),
		}); err != nil {
			t.Fatalf("SetTopicAttributes(%s): %v", name, err)
		}
	}

	attrs := topicAttributes(t, client, aws.ToString(topic.TopicArn))

	for name, want := range sets {
		if got := attrs[name]; got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
}

// TestSDKCreateTopicFeedbackAttributes guards that the feedback family and
// SignatureVersion/TracingConfig/ArchivePolicy can also be set at CreateTopic
// time (real SNS accepts an Attributes map on create), not just via a later
// SetTopicAttributes call.
func TestSDKCreateTopicFeedbackAttributes(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	topic, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{
		Name: aws.String("create-time-feedback"),
		Attributes: map[string]string{
			"SignatureVersion":                "2",
			"TracingConfig":                   "Active",
			"LambdaSuccessFeedbackRoleArn":    "arn:aws:iam::123456789012:role/sns-feedback",
			"LambdaSuccessFeedbackSampleRate": "50",
			"LambdaFailureFeedbackRoleArn":    "arn:aws:iam::123456789012:role/sns-feedback",
		},
	})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	attrs := topicAttributes(t, client, aws.ToString(topic.TopicArn))

	want := map[string]string{
		"SignatureVersion":                "2",
		"TracingConfig":                   "Active",
		"LambdaSuccessFeedbackRoleArn":    "arn:aws:iam::123456789012:role/sns-feedback",
		"LambdaSuccessFeedbackSampleRate": "50",
		"LambdaFailureFeedbackRoleArn":    "arn:aws:iam::123456789012:role/sns-feedback",
	}

	for name, wantVal := range want {
		if got := attrs[name]; got != wantVal {
			t.Fatalf("%s = %q, want %q", name, got, wantVal)
		}
	}
}

// TestSDKGetTopicAttributesOmitsUnsetFeedbackAttributes guards the AWS
// response-inclusion rule: an attribute that was never set must be absent
// from GetTopicAttributes entirely, not echoed as an empty string.
func TestSDKGetTopicAttributesOmitsUnsetFeedbackAttributes(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	topic, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("no-feedback")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	attrs := topicAttributes(t, client, aws.ToString(topic.TopicArn))

	unset := []string{
		"SignatureVersion", "TracingConfig", "ArchivePolicy",
		"SQSSuccessFeedbackRoleArn", "SQSFailureFeedbackRoleArn", "SQSSuccessFeedbackSampleRate",
		"HTTPSuccessFeedbackRoleArn", "HTTPFailureFeedbackRoleArn", "HTTPSuccessFeedbackSampleRate",
		"LambdaSuccessFeedbackRoleArn", "LambdaFailureFeedbackRoleArn", "LambdaSuccessFeedbackSampleRate",
		"ApplicationSuccessFeedbackRoleArn", "ApplicationFailureFeedbackRoleArn", "ApplicationSuccessFeedbackSampleRate",
		"FirehoseSuccessFeedbackRoleArn", "FirehoseFailureFeedbackRoleArn", "FirehoseSuccessFeedbackSampleRate",
	}

	for _, name := range unset {
		if _, present := attrs[name]; present {
			t.Fatalf("GetTopicAttributes includes unset attribute %q, want absent", name)
		}
	}
}
