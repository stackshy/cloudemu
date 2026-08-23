package sqs_test

import (
	"context"
	"crypto/md5" //nolint:gosec // asserting the wire checksum
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

func TestSDKSQSCreateQueueAttributesStored(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	q, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{
		QueueName: aws.String("cfg-q"),
		Attributes: map[string]string{
			"VisibilityTimeout": "45",
			"DelaySeconds":      "7",
		},
	})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	got, err := client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl:       q.QueueUrl,
		AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameAll},
	})
	if err != nil {
		t.Fatalf("GetQueueAttributes: %v", err)
	}

	checks := map[string]string{
		"VisibilityTimeout":      "45",
		"DelaySeconds":           "7",
		"MaximumMessageSize":     "262144",
		"MessageRetentionPeriod": "345600",
	}
	for k, want := range checks {
		if got.Attributes[k] != want {
			t.Errorf("attr %s = %q, want %q", k, got.Attributes[k], want)
		}
	}

	// Standard queues must not advertise FifoQueue.
	if _, ok := got.Attributes["FifoQueue"]; ok {
		t.Errorf("standard queue should omit FifoQueue, got %q", got.Attributes["FifoQueue"])
	}
}

func TestSDKSQSMessageAttributesAndSystemAttributes(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	q, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("attrs-q")})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	body := "hello world"
	send, err := client.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl:    q.QueueUrl,
		MessageBody: aws.String(body),
		MessageAttributes: map[string]types.MessageAttributeValue{
			"color": {DataType: aws.String("String"), StringValue: aws.String("red")},
		},
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	wantBodyMD5 := md5Hex(body)
	if aws.ToString(send.MD5OfMessageBody) != wantBodyMD5 {
		t.Errorf("MD5OfMessageBody = %q, want %q", aws.ToString(send.MD5OfMessageBody), wantBodyMD5)
	}

	if aws.ToString(send.MD5OfMessageAttributes) == "" {
		t.Error("MD5OfMessageAttributes empty, want non-empty")
	}

	rcv, err := client.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl:                    q.QueueUrl,
		MaxNumberOfMessages:         1,
		MessageAttributeNames:       []string{"All"},
		MessageSystemAttributeNames: []types.MessageSystemAttributeName{types.MessageSystemAttributeNameAll},
	})
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}

	if len(rcv.Messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(rcv.Messages))
	}

	m := rcv.Messages[0]
	if aws.ToString(m.MD5OfBody) != wantBodyMD5 {
		t.Errorf("MD5OfBody = %q, want %q", aws.ToString(m.MD5OfBody), wantBodyMD5)
	}

	color, ok := m.MessageAttributes["color"]
	if !ok {
		t.Fatal("MessageAttributes missing color")
	}

	if aws.ToString(color.StringValue) != "red" {
		t.Errorf("color = %q, want red", aws.ToString(color.StringValue))
	}

	if m.Attributes["ApproximateReceiveCount"] != "1" {
		t.Errorf("ApproximateReceiveCount = %q, want 1", m.Attributes["ApproximateReceiveCount"])
	}

	if m.Attributes["SentTimestamp"] == "" {
		t.Error("SentTimestamp missing")
	}

	if m.Attributes["SenderId"] == "" {
		t.Error("SenderId missing")
	}
}

func TestSDKSQSDLQRedrive(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	dlq, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("dlq")})
	if err != nil {
		t.Fatalf("create dlq: %v", err)
	}

	dlqAttrs, err := client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl:       dlq.QueueUrl,
		AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameQueueArn},
	})
	if err != nil {
		t.Fatalf("get dlq arn: %v", err)
	}

	dlqArn := dlqAttrs.Attributes["QueueArn"]

	main, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("main")})
	if err != nil {
		t.Fatalf("create main: %v", err)
	}

	redrive := fmt.Sprintf(`{"deadLetterTargetArn":%q,"maxReceiveCount":2}`, dlqArn)
	if _, err := client.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{
		QueueUrl: main.QueueUrl,
		Attributes: map[string]string{
			"RedrivePolicy":     redrive,
			"VisibilityTimeout": "0",
		},
	}); err != nil {
		t.Fatalf("SetQueueAttributes: %v", err)
	}

	// Confirm the RedrivePolicy is echoed back.
	got, err := client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl:       main.QueueUrl,
		AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameRedrivePolicy},
	})
	if err != nil {
		t.Fatalf("get redrive: %v", err)
	}

	if got.Attributes["RedrivePolicy"] != redrive {
		t.Errorf("RedrivePolicy = %q, want %q", got.Attributes["RedrivePolicy"], redrive)
	}

	if _, err := client.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl:    main.QueueUrl,
		MessageBody: aws.String("poison"),
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	// Receive past maxReceiveCount without deleting; the message redrives to the DLQ.
	for i := 0; i < 3; i++ {
		if _, err := client.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
			QueueUrl:            main.QueueUrl,
			MaxNumberOfMessages: 1,
		}); err != nil {
			t.Fatalf("receive %d: %v", i, err)
		}
	}

	dlqRcv, err := client.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl:            dlq.QueueUrl,
		MaxNumberOfMessages: 1,
	})
	if err != nil {
		t.Fatalf("receive dlq: %v", err)
	}

	if len(dlqRcv.Messages) != 1 {
		t.Fatalf("DLQ has %d messages, want 1 (redrive did not fire)", len(dlqRcv.Messages))
	}

	if aws.ToString(dlqRcv.Messages[0].Body) != "poison" {
		t.Errorf("DLQ body = %q, want poison", aws.ToString(dlqRcv.Messages[0].Body))
	}
}

func TestSDKSQSFifoSequenceNumber(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	q, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{
		QueueName:  aws.String("orders.fifo"),
		Attributes: map[string]string{"FifoQueue": "true"},
	})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	out, err := client.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl:               q.QueueUrl,
		MessageBody:            aws.String("m1"),
		MessageGroupId:         aws.String("g1"),
		MessageDeduplicationId: aws.String("d1"),
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	if aws.ToString(out.SequenceNumber) == "" {
		t.Error("FIFO SendMessage returned empty SequenceNumber")
	}
}

func TestSDKSQSChangeMessageVisibilityBatch(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	q, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("cmvb")})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	if _, err := client.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl: q.QueueUrl, MessageBody: aws.String("m"),
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	rcv, err := client.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl: q.QueueUrl, MaxNumberOfMessages: 1,
	})
	if err != nil || len(rcv.Messages) != 1 {
		t.Fatalf("ReceiveMessage: %v (n=%d)", err, len(rcv.Messages))
	}

	out, err := client.ChangeMessageVisibilityBatch(ctx, &awssqs.ChangeMessageVisibilityBatchInput{
		QueueUrl: q.QueueUrl,
		Entries: []types.ChangeMessageVisibilityBatchRequestEntry{
			{Id: aws.String("1"), ReceiptHandle: rcv.Messages[0].ReceiptHandle, VisibilityTimeout: 30},
		},
	})
	if err != nil {
		t.Fatalf("ChangeMessageVisibilityBatch: %v", err)
	}

	if len(out.Successful) != 1 {
		t.Fatalf("Successful = %d, want 1", len(out.Successful))
	}
}

func TestSDKSQSListQueuesPagination(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	for _, n := range []string{"p1", "p2", "p3"} {
		if _, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String(n)}); err != nil {
			t.Fatalf("create %s: %v", n, err)
		}
	}

	first, err := client.ListQueues(ctx, &awssqs.ListQueuesInput{MaxResults: aws.Int32(2)})
	if err != nil {
		t.Fatalf("ListQueues page 1: %v", err)
	}

	if len(first.QueueUrls) != 2 {
		t.Fatalf("page 1 = %d urls, want 2", len(first.QueueUrls))
	}

	if aws.ToString(first.NextToken) == "" {
		t.Fatal("page 1 missing NextToken")
	}

	second, err := client.ListQueues(ctx, &awssqs.ListQueuesInput{
		MaxResults: aws.Int32(2),
		NextToken:  first.NextToken,
	})
	if err != nil {
		t.Fatalf("ListQueues page 2: %v", err)
	}

	if len(second.QueueUrls) != 1 {
		t.Fatalf("page 2 = %d urls, want 1", len(second.QueueUrls))
	}
}

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s)) //nolint:gosec // wire checksum
	return hex.EncodeToString(sum[:])
}
