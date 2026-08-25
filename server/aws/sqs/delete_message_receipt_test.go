package sqs_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
)

// TestSDKSQSDeleteMessageMalformedHandle asserts a syntactically-invalid receipt
// handle is rejected with ReceiptHandleIsInvalid, while a stale but well-formed
// handle stays an idempotent no-op (200).
func TestSDKSQSDeleteMessageMalformedHandle(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	q, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("del-malformed-q")})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	_, err = client.DeleteMessage(ctx, &awssqs.DeleteMessageInput{
		QueueUrl:      q.QueueUrl,
		ReceiptHandle: aws.String("!!!not-a-handle!!!"),
	})
	if code := apiErrorCode(t, err); code != "ReceiptHandleIsInvalid" {
		t.Fatalf("malformed handle: error code = %q, want ReceiptHandleIsInvalid", code)
	}

	// A stale but well-formed handle remains an idempotent success.
	if _, err := client.DeleteMessage(ctx, &awssqs.DeleteMessageInput{
		QueueUrl:      q.QueueUrl,
		ReceiptHandle: aws.String("AQEBstalebutwellformed1234567890"),
	}); err != nil {
		t.Fatalf("stale well-formed handle should succeed, got: %v", err)
	}
}
