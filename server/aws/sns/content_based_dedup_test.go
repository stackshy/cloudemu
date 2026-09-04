package sns_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	smithy "github.com/aws/smithy-go"
)

// TestSDKSetTopicAttributesContentBasedDeduplicationRejectedOnStandardTopic
// guards real AWS's rule that ContentBasedDeduplication is only valid on a
// FIFO topic: SetTopicAttributes naming it on a standard topic must fail with
// InvalidParameter, not silently succeed and leave a Terraform diff.
func TestSDKSetTopicAttributesContentBasedDeduplicationRejectedOnStandardTopic(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	topic, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("standard-cbd")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	_, err = client.SetTopicAttributes(ctx, &awssns.SetTopicAttributesInput{
		TopicArn:       topic.TopicArn,
		AttributeName:  aws.String("ContentBasedDeduplication"),
		AttributeValue: aws.String("true"),
	})
	if err == nil {
		t.Fatal("SetTopicAttributes(ContentBasedDeduplication) on standard topic succeeded, want InvalidParameter")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not a smithy.APIError: %v", err)
	}

	if apiErr.ErrorCode() != "InvalidParameter" {
		t.Fatalf("error code = %q, want InvalidParameter", apiErr.ErrorCode())
	}
}

// TestSDKSetTopicAttributesContentBasedDeduplicationAllowedOnFifoTopic is the
// positive counterpart: the same call on a .fifo topic must still succeed.
func TestSDKSetTopicAttributesContentBasedDeduplicationAllowedOnFifoTopic(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	topic, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{
		Name:       aws.String("fifo-cbd.fifo"),
		Attributes: map[string]string{"FifoTopic": "true"},
	})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	if _, err := client.SetTopicAttributes(ctx, &awssns.SetTopicAttributesInput{
		TopicArn:       topic.TopicArn,
		AttributeName:  aws.String("ContentBasedDeduplication"),
		AttributeValue: aws.String("true"),
	}); err != nil {
		t.Fatalf("SetTopicAttributes(ContentBasedDeduplication) on FIFO topic: %v", err)
	}
}
