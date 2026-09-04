package sns_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
)

// topicAttributes fetches a topic's GetTopicAttributes map.
func topicAttributes(t *testing.T, sns *awssns.Client, arn string) map[string]string {
	t.Helper()

	out, err := sns.GetTopicAttributes(context.Background(), &awssns.GetTopicAttributesInput{
		TopicArn: aws.String(arn),
	})
	if err != nil {
		t.Fatalf("GetTopicAttributes: %v", err)
	}

	return out.Attributes
}

// TestSDKSNSCreateTopicDisplayName asserts CreateTopic Attributes.DisplayName is
// persisted and echoed by GetTopicAttributes (was dropped at create time).
func TestSDKSNSCreateTopicDisplayName(t *testing.T) {
	sns := newSDKClient(t)
	ctx := context.Background()

	topic, err := sns.CreateTopic(ctx, &awssns.CreateTopicInput{
		Name:       aws.String("named"),
		Attributes: map[string]string{"DisplayName": "My Topic"},
	})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	if got := topicAttributes(t, sns, aws.ToString(topic.TopicArn))["DisplayName"]; got != "My Topic" {
		t.Fatalf("DisplayName = %q, want %q", got, "My Topic")
	}
}

// TestSDKSNSCreateTopicFifoFlags asserts a .fifo topic created with FifoTopic and
// ContentBasedDeduplication persists both flags for GetTopicAttributes to reflect.
func TestSDKSNSCreateTopicFifoFlags(t *testing.T) {
	sns := newSDKClient(t)
	ctx := context.Background()

	topic, err := sns.CreateTopic(ctx, &awssns.CreateTopicInput{
		Name: aws.String("orders.fifo"),
		Attributes: map[string]string{
			"FifoTopic":                 "true",
			"ContentBasedDeduplication": "true",
		},
	})
	if err != nil {
		t.Fatalf("CreateTopic(fifo): %v", err)
	}

	attrs := topicAttributes(t, sns, aws.ToString(topic.TopicArn))

	if attrs["FifoTopic"] != "true" {
		t.Fatalf("FifoTopic = %q, want true", attrs["FifoTopic"])
	}

	if attrs["ContentBasedDeduplication"] != "true" {
		t.Fatalf("ContentBasedDeduplication = %q, want true", attrs["ContentBasedDeduplication"])
	}
}

// TestSDKSNSCreateTopicFifoNameRequiresSuffix guards real SNS's documented
// naming rule (API_CreateTopic.html: "For a FIFO topic, the name must end with
// the .fifo suffix") — FifoTopic=true with a non-.fifo name must be rejected.
func TestSDKSNSCreateTopicFifoNameRequiresSuffix(t *testing.T) {
	sns := newSDKClient(t)
	ctx := context.Background()

	_, err := sns.CreateTopic(ctx, &awssns.CreateTopicInput{
		Name:       aws.String("not-fifo-named"),
		Attributes: map[string]string{"FifoTopic": "true"},
	})
	if err == nil {
		t.Fatal("CreateTopic(FifoTopic=true, non-.fifo name) succeeded, want InvalidParameter")
	}
}

// TestSDKSNSSetTopicAttributesContentBasedDeduplication is a regression guard:
// SetTopicAttributes previously only forwarded DisplayName/Policy, silently
// dropping ContentBasedDeduplication (and DeliveryPolicy/KmsMasterKeyId), which
// caused aws_sns_topic's content_based_deduplication to perpetually diff under
// Terraform since the value never persisted.
func TestSDKSNSSetTopicAttributesContentBasedDeduplication(t *testing.T) {
	sns := newSDKClient(t)
	ctx := context.Background()

	topic, err := sns.CreateTopic(ctx, &awssns.CreateTopicInput{
		Name:       aws.String("cbd.fifo"),
		Attributes: map[string]string{"FifoTopic": "true"},
	})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	if _, err := sns.SetTopicAttributes(ctx, &awssns.SetTopicAttributesInput{
		TopicArn:       topic.TopicArn,
		AttributeName:  aws.String("ContentBasedDeduplication"),
		AttributeValue: aws.String("true"),
	}); err != nil {
		t.Fatalf("SetTopicAttributes(ContentBasedDeduplication): %v", err)
	}

	if got := topicAttributes(t, sns, aws.ToString(topic.TopicArn))["ContentBasedDeduplication"]; got != "true" {
		t.Fatalf("ContentBasedDeduplication after SetTopicAttributes = %q, want true", got)
	}

	if _, err := sns.SetTopicAttributes(ctx, &awssns.SetTopicAttributesInput{
		TopicArn:       topic.TopicArn,
		AttributeName:  aws.String("DeliveryPolicy"),
		AttributeValue: aws.String(`{"http":{"defaultHealthyRetryPolicy":{"numRetries":5}}}`),
	}); err != nil {
		t.Fatalf("SetTopicAttributes(DeliveryPolicy): %v", err)
	}

	if got := topicAttributes(t, sns, aws.ToString(topic.TopicArn))["DeliveryPolicy"]; got == "" {
		t.Fatal("DeliveryPolicy after SetTopicAttributes is empty, want it persisted")
	}
}
