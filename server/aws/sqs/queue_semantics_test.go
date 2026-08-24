package sqs_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	smithy "github.com/aws/smithy-go"
)

// apiErrorCode extracts the SQS API error code from an SDK error.
func apiErrorCode(t *testing.T, err error) string {
	t.Helper()

	if err == nil {
		t.Fatal("expected an API error, got nil")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not an smithy.APIError: %v", err)
	}

	return apiErr.ErrorCode()
}

// CreateQueue is idempotent for identical attributes and only rejects a
// re-create when the attributes differ.
func TestSDKSQSCreateQueueIdempotent(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	in := &awssqs.CreateQueueInput{
		QueueName:  aws.String("idem-q"),
		Attributes: map[string]string{"VisibilityTimeout": "40"},
	}

	first, err := client.CreateQueue(ctx, in)
	if err != nil {
		t.Fatalf("first CreateQueue: %v", err)
	}

	second, err := client.CreateQueue(ctx, in)
	if err != nil {
		t.Fatalf("idempotent CreateQueue should succeed, got: %v", err)
	}

	if aws.ToString(first.QueueUrl) != aws.ToString(second.QueueUrl) {
		t.Fatalf("idempotent re-create returned a different URL: %q vs %q",
			aws.ToString(first.QueueUrl), aws.ToString(second.QueueUrl))
	}

	_, err = client.CreateQueue(ctx, &awssqs.CreateQueueInput{
		QueueName:  aws.String("idem-q"),
		Attributes: map[string]string{"VisibilityTimeout": "99"},
	})
	if code := apiErrorCode(t, err); code != "QueueNameExists" {
		t.Fatalf("re-create with different attributes: error code = %q, want QueueNameExists", code)
	}
}

// DeleteMessage against an existing queue with an unknown (but well-formed)
// receipt handle succeeds; only a missing queue is an error.
func TestSDKSQSDeleteMessageUnknownHandleSucceeds(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	q, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("del-q")})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	if _, err := client.DeleteMessage(ctx, &awssqs.DeleteMessageInput{
		QueueUrl:      q.QueueUrl,
		ReceiptHandle: aws.String("AQEBbogushandle1234567890"),
	}); err != nil {
		t.Fatalf("DeleteMessage with stale handle should succeed, got: %v", err)
	}
}

// ChangeMessageVisibility against an existing queue with an unknown handle
// returns ReceiptHandleIsInvalid, not QueueDoesNotExist.
func TestSDKSQSChangeMessageVisibilityUnknownHandle(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	q, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("cmv-q")})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	_, err = client.ChangeMessageVisibility(ctx, &awssqs.ChangeMessageVisibilityInput{
		QueueUrl:          q.QueueUrl,
		ReceiptHandle:     aws.String("AQEBbogushandle1234567890"),
		VisibilityTimeout: 10,
	})
	if code := apiErrorCode(t, err); code != "ReceiptHandleIsInvalid" {
		t.Fatalf("ChangeMessageVisibility unknown handle: error code = %q, want ReceiptHandleIsInvalid", code)
	}
}

// A queue created with VisibilityTimeout "0" keeps 0 (0 is a valid value, not
// "unset").
func TestSDKSQSCreateQueueZeroVisibility(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	q, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{
		QueueName:  aws.String("vz-q"),
		Attributes: map[string]string{"VisibilityTimeout": "0"},
	})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	got, err := client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl:       q.QueueUrl,
		AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameVisibilityTimeout},
	})
	if err != nil {
		t.Fatalf("GetQueueAttributes: %v", err)
	}

	if got.Attributes["VisibilityTimeout"] != "0" {
		t.Fatalf("VisibilityTimeout = %q, want 0", got.Attributes["VisibilityTimeout"])
	}
}

// A queue that omits VisibilityTimeout still defaults to 30.
func TestSDKSQSCreateQueueDefaultVisibility(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	q, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("dv-q")})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	got, err := client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl:       q.QueueUrl,
		AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameVisibilityTimeout},
	})
	if err != nil {
		t.Fatalf("GetQueueAttributes: %v", err)
	}

	if got.Attributes["VisibilityTimeout"] != "30" {
		t.Fatalf("default VisibilityTimeout = %q, want 30", got.Attributes["VisibilityTimeout"])
	}
}

// SendMessage rejects a body larger than the queue's MaximumMessageSize with
// InvalidParameterValue.
func TestSDKSQSSendMessageTooLong(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	q, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("big-q")})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	body := strings.Repeat("x", 300*1024)

	_, err = client.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl:    q.QueueUrl,
		MessageBody: aws.String(body),
	})
	if code := apiErrorCode(t, err); code != "InvalidParameterValue" {
		t.Fatalf("oversize SendMessage: error code = %q, want InvalidParameterValue", code)
	}
}
