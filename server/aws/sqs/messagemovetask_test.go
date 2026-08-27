package sqs_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// queueArn resolves a queue's ARN via GetQueueAttributes.
func queueArn(t *testing.T, client *awssqs.Client, url *string) string {
	t.Helper()

	attrs, err := client.GetQueueAttributes(context.Background(), &awssqs.GetQueueAttributesInput{
		QueueUrl:       url,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})
	if err != nil {
		t.Fatalf("GetQueueAttributes: %v", err)
	}

	return attrs.Attributes["QueueArn"]
}

func TestSDKSQSMessageMoveTaskRoundTrip(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	dlq, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("dlq")})
	if err != nil {
		t.Fatalf("CreateQueue dlq: %v", err)
	}

	dest, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("dest")})
	if err != nil {
		t.Fatalf("CreateQueue dest: %v", err)
	}

	for i := 0; i < 3; i++ {
		if _, err := client.SendMessage(ctx, &awssqs.SendMessageInput{
			QueueUrl: dlq.QueueUrl, MessageBody: aws.String("stuck"),
		}); err != nil {
			t.Fatalf("SendMessage: %v", err)
		}
	}

	dlqArn := queueArn(t, client, dlq.QueueUrl)
	destArn := queueArn(t, client, dest.QueueUrl)

	start, err := client.StartMessageMoveTask(ctx, &awssqs.StartMessageMoveTaskInput{
		SourceArn:      aws.String(dlqArn),
		DestinationArn: aws.String(destArn),
	})
	if err != nil {
		t.Fatalf("StartMessageMoveTask: %v", err)
	}

	if aws.ToString(start.TaskHandle) == "" {
		t.Fatal("StartMessageMoveTask returned empty TaskHandle")
	}

	list, err := client.ListMessageMoveTasks(ctx, &awssqs.ListMessageMoveTasksInput{
		SourceArn: aws.String(dlqArn),
	})
	if err != nil {
		t.Fatalf("ListMessageMoveTasks: %v", err)
	}

	if len(list.Results) != 1 {
		t.Fatalf("ListMessageMoveTasks returned %d results, want 1", len(list.Results))
	}

	res := list.Results[0]
	if aws.ToString(res.Status) != "COMPLETED" {
		t.Fatalf("Status = %q, want COMPLETED", aws.ToString(res.Status))
	}

	if res.ApproximateNumberOfMessagesMoved != 3 {
		t.Fatalf("ApproximateNumberOfMessagesMoved = %d, want 3", res.ApproximateNumberOfMessagesMoved)
	}

	if aws.ToInt64(res.ApproximateNumberOfMessagesToMove) != 3 {
		t.Fatalf("ApproximateNumberOfMessagesToMove = %d, want 3", aws.ToInt64(res.ApproximateNumberOfMessagesToMove))
	}

	// The destination queue now holds the redriven messages.
	rcv, err := client.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl: dest.QueueUrl, MaxNumberOfMessages: 10,
	})
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}

	if len(rcv.Messages) != 3 {
		t.Fatalf("destination has %d messages, want 3", len(rcv.Messages))
	}
}

func TestSDKSQSCancelMessageMoveTaskCompleted(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	dlq, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("dlq")})
	if err != nil {
		t.Fatalf("CreateQueue dlq: %v", err)
	}

	dest, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("dest")})
	if err != nil {
		t.Fatalf("CreateQueue dest: %v", err)
	}

	if _, err := client.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl: dlq.QueueUrl, MessageBody: aws.String("x"),
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	dlqArn := queueArn(t, client, dlq.QueueUrl)
	destArn := queueArn(t, client, dest.QueueUrl)

	start, err := client.StartMessageMoveTask(ctx, &awssqs.StartMessageMoveTaskInput{
		SourceArn:      aws.String(dlqArn),
		DestinationArn: aws.String(destArn),
	})
	if err != nil {
		t.Fatalf("StartMessageMoveTask: %v", err)
	}

	// Synchronous completion means the task is already COMPLETED, so canceling it
	// returns ResourceNotFoundException.
	if _, err := client.CancelMessageMoveTask(ctx, &awssqs.CancelMessageMoveTaskInput{
		TaskHandle: start.TaskHandle,
	}); err == nil {
		t.Fatal("CancelMessageMoveTask on completed task returned nil error, want ResourceNotFoundException")
	}
}
