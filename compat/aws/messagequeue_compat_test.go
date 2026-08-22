package aws

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

var errNoMessages = errors.New("expected at least one message, got none")

// TestAWSMessageQueueCompat drives an SQS queue + message lifecycle through the
// real aws-sdk-go-v2 client, including batch send/delete and visibility change.
// Operation names match the portable "messagequeue" driver (ReceiveMessage →
// "ReceiveMessages", ChangeMessageVisibility → "ChangeVisibility").
func TestAWSMessageQueueCompat(t *testing.T) {
	provider := cloudemu.NewAWS()
	sess := compat.BootAWS(t, awsserver.Drivers{SQS: provider.SQS})
	client := sess.SQSClient()
	ctx := context.Background()

	const svc = "messagequeue"

	var queueURL string

	sess.Op(svc, "CreateQueue", func() error {
		out, err := client.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String("compat-queue")})
		if err != nil {
			return err
		}

		queueURL = aws.ToString(out.QueueUrl)

		return nil
	})

	sess.Op(svc, "ListQueues", func() error {
		_, err := client.ListQueues(ctx, &sqs.ListQueuesInput{})
		return err
	})

	sess.Op(svc, "GetQueueAttributes", func() error {
		_, err := client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
			QueueUrl:       aws.String(queueURL),
			AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameAll},
		})

		return err
	})

	sess.Op(svc, "SetQueueAttributes", func() error {
		_, err := client.SetQueueAttributes(ctx, &sqs.SetQueueAttributesInput{
			QueueUrl:   aws.String(queueURL),
			Attributes: map[string]string{string(sqstypes.QueueAttributeNameVisibilityTimeout): "30"},
		})

		return err
	})

	sess.Op(svc, "SendMessage", func() error {
		_, err := client.SendMessage(ctx, &sqs.SendMessageInput{
			QueueUrl:    aws.String(queueURL),
			MessageBody: aws.String("hello cloudemu"),
		})

		return err
	})

	var handle string

	sess.Op(svc, "ReceiveMessages", func() error {
		out, err := client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(queueURL),
			MaxNumberOfMessages: 10,
		})
		if err != nil {
			return err
		}

		if len(out.Messages) == 0 {
			return errNoMessages
		}

		handle = aws.ToString(out.Messages[0].ReceiptHandle)

		return nil
	})

	sess.Op(svc, "ChangeVisibility", func() error {
		_, err := client.ChangeMessageVisibility(ctx, &sqs.ChangeMessageVisibilityInput{
			QueueUrl:          aws.String(queueURL),
			ReceiptHandle:     aws.String(handle),
			VisibilityTimeout: 10,
		})

		return err
	})

	sess.Op(svc, "DeleteMessage", func() error {
		_, err := client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
			QueueUrl:      aws.String(queueURL),
			ReceiptHandle: aws.String(handle),
		})

		return err
	})

	sess.Op(svc, "SendMessageBatch", func() error {
		_, err := client.SendMessageBatch(ctx, &sqs.SendMessageBatchInput{
			QueueUrl: aws.String(queueURL),
			Entries: []sqstypes.SendMessageBatchRequestEntry{
				{Id: aws.String("1"), MessageBody: aws.String("m1")},
				{Id: aws.String("2"), MessageBody: aws.String("m2")},
			},
		})

		return err
	})

	sess.Op(svc, "DeleteMessageBatch", func() error {
		recv, err := client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(queueURL),
			MaxNumberOfMessages: 10,
		})
		if err != nil {
			return err
		}

		if len(recv.Messages) == 0 {
			return errNoMessages
		}

		entries := make([]sqstypes.DeleteMessageBatchRequestEntry, 0, len(recv.Messages))
		for i := range recv.Messages {
			entries = append(entries, sqstypes.DeleteMessageBatchRequestEntry{
				Id:            aws.String(strconv.Itoa(i)),
				ReceiptHandle: recv.Messages[i].ReceiptHandle,
			})
		}

		_, err = client.DeleteMessageBatch(ctx, &sqs.DeleteMessageBatchInput{
			QueueUrl: aws.String(queueURL),
			Entries:  entries,
		})

		return err
	})

	sess.Op(svc, "PurgeQueue", func() error {
		_, err := client.PurgeQueue(ctx, &sqs.PurgeQueueInput{QueueUrl: aws.String(queueURL)})
		return err
	})

	sess.Op(svc, "DeleteQueue", func() error {
		_, err := client.DeleteQueue(ctx, &sqs.DeleteQueueInput{QueueUrl: aws.String(queueURL)})
		return err
	})
}
