package sqs_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// mustCreateQueue creates a standard queue and returns its URL.
func mustCreateQueue(t *testing.T, client *awssqs.Client, name string) string {
	t.Helper()

	out, err := client.CreateQueue(context.Background(), &awssqs.CreateQueueInput{QueueName: aws.String(name)})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	return aws.ToString(out.QueueUrl)
}

// TestSDKSendMessageBatchDuplicateIDs covers finding 1: two entries sharing an Id
// must reject the whole batch with BatchEntryIdsNotDistinct and enqueue nothing.
func TestSDKSendMessageBatchDuplicateIDs(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()
	url := mustCreateQueue(t, client, "dup-batch")

	_, err := client.SendMessageBatch(ctx, &awssqs.SendMessageBatchInput{
		QueueUrl: aws.String(url),
		Entries: []types.SendMessageBatchRequestEntry{
			{Id: aws.String("a"), MessageBody: aws.String("body-a")},
			{Id: aws.String("a"), MessageBody: aws.String("body-b")},
		},
	})
	if code := apiErrorCode(t, err); code != "BatchEntryIdsNotDistinct" {
		t.Fatalf("error code = %q, want BatchEntryIdsNotDistinct", code)
	}

	// The batch must enqueue nothing.
	recv, err := client.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl:            aws.String(url),
		MaxNumberOfMessages: 10,
	})
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}

	if len(recv.Messages) != 0 {
		t.Fatalf("received %d messages, want 0 (rejected batch must enqueue nothing)", len(recv.Messages))
	}
}

// TestSDKSendMessageBatchTooManyEntries covers finding 2: more than 10 entries
// must reject with TooManyEntriesInBatchRequest.
func TestSDKSendMessageBatchTooManyEntries(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()
	url := mustCreateQueue(t, client, "too-many")

	entries := make([]types.SendMessageBatchRequestEntry, 11)
	for i := range entries {
		entries[i] = types.SendMessageBatchRequestEntry{
			Id:          aws.String(fmt.Sprintf("id-%d", i)),
			MessageBody: aws.String(fmt.Sprintf("body-%d", i)),
		}
	}

	_, err := client.SendMessageBatch(ctx, &awssqs.SendMessageBatchInput{
		QueueUrl: aws.String(url),
		Entries:  entries,
	})
	if code := apiErrorCode(t, err); code != "TooManyEntriesInBatchRequest" {
		t.Fatalf("error code = %q, want TooManyEntriesInBatchRequest", code)
	}
}

// TestSDKSendMessageBatchEmpty covers finding 3: an empty batch must reject with
// EmptyBatchRequest across all three batch operations.
func TestSDKSendMessageBatchEmpty(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()
	url := mustCreateQueue(t, client, "empty-batch")

	_, sendErr := client.SendMessageBatch(ctx, &awssqs.SendMessageBatchInput{
		QueueUrl: aws.String(url),
		Entries:  []types.SendMessageBatchRequestEntry{},
	})
	if code := apiErrorCode(t, sendErr); code != "EmptyBatchRequest" {
		t.Fatalf("SendMessageBatch error code = %q, want EmptyBatchRequest", code)
	}

	_, delErr := client.DeleteMessageBatch(ctx, &awssqs.DeleteMessageBatchInput{
		QueueUrl: aws.String(url),
		Entries:  []types.DeleteMessageBatchRequestEntry{},
	})
	if code := apiErrorCode(t, delErr); code != "EmptyBatchRequest" {
		t.Fatalf("DeleteMessageBatch error code = %q, want EmptyBatchRequest", code)
	}

	_, visErr := client.ChangeMessageVisibilityBatch(ctx, &awssqs.ChangeMessageVisibilityBatchInput{
		QueueUrl: aws.String(url),
		Entries:  []types.ChangeMessageVisibilityBatchRequestEntry{},
	})
	if code := apiErrorCode(t, visErr); code != "EmptyBatchRequest" {
		t.Fatalf("ChangeMessageVisibilityBatch error code = %q, want EmptyBatchRequest", code)
	}
}

// TestSDKCreateQueueAttributeOutOfRange covers finding 4: an out-of-range numeric
// queue attribute must reject with InvalidAttributeValue at CreateQueue time.
func TestSDKCreateQueueAttributeOutOfRange(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	_, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{
		QueueName:  aws.String("bad-vt"),
		Attributes: map[string]string{"VisibilityTimeout": "99999"},
	})
	if code := apiErrorCode(t, err); code != "InvalidAttributeValue" {
		t.Fatalf("error code = %q, want InvalidAttributeValue", code)
	}
}

// TestSDKSetQueueAttributesOutOfRange covers finding 4: SetQueueAttributes must
// reject an out-of-range value with InvalidAttributeValue.
func TestSDKSetQueueAttributesOutOfRange(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()
	url := mustCreateQueue(t, client, "set-attr-range")

	cases := map[string]string{
		"DelaySeconds":           "901",
		"MessageRetentionPeriod": "30",
		"MaximumMessageSize":     "512",
		"VisibilityTimeout":      "99999",
	}

	for name, value := range cases {
		_, err := client.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{
			QueueUrl:   aws.String(url),
			Attributes: map[string]string{name: value},
		})
		if code := apiErrorCode(t, err); code != "InvalidAttributeValue" {
			t.Fatalf("%s=%s: error code = %q, want InvalidAttributeValue", name, value, code)
		}
	}
}

// TestSDKSetQueueAttributesInRange guards the happy path: valid values still apply.
func TestSDKSetQueueAttributesInRange(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()
	url := mustCreateQueue(t, client, "set-attr-ok")

	if _, err := client.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{
		QueueUrl:   aws.String(url),
		Attributes: map[string]string{"VisibilityTimeout": "60", "DelaySeconds": "10"},
	}); err != nil {
		t.Fatalf("SetQueueAttributes with valid values: %v", err)
	}
}
