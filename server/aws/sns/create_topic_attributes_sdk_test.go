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
