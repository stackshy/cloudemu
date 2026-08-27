package sns_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
)

// DeleteTopic is idempotent in SNS: deleting a topic that does not exist (or was
// already deleted) returns success rather than an error.
func TestSDKSNSDeleteTopicIdempotent(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("fan-topic")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	arn := created.TopicArn

	if _, err := client.DeleteTopic(ctx, &awssns.DeleteTopicInput{TopicArn: arn}); err != nil {
		t.Fatalf("first DeleteTopic: %v", err)
	}

	// Deleting an already-deleted topic must still succeed.
	if _, err := client.DeleteTopic(ctx, &awssns.DeleteTopicInput{TopicArn: arn}); err != nil {
		t.Fatalf("idempotent DeleteTopic should succeed, got: %v", err)
	}

	// Deleting a never-existed topic must also succeed.
	if _, err := client.DeleteTopic(ctx, &awssns.DeleteTopicInput{
		TopicArn: aws.String("arn:aws:sns:us-east-1:123456789012:never-existed"),
	}); err != nil {
		t.Fatalf("DeleteTopic on non-existent topic should succeed, got: %v", err)
	}
}
