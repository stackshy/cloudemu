package sqs_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newSDKClient(t *testing.T) (*awssqs.Client, *cloudemuAWSHandle) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{SQS: cloud.SQS})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	client := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})

	return client, &cloudemuAWSHandle{cloud: cloud}
}

type cloudemuAWSHandle struct {
	cloud any
}

func TestSDKSQSCreateAndGet(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	out, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{
		QueueName: aws.String("sdk-q"),
	})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	if aws.ToString(out.QueueUrl) == "" {
		t.Fatal("CreateQueue returned empty QueueUrl")
	}

	got, err := client.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{
		QueueName: aws.String("sdk-q"),
	})
	if err != nil {
		t.Fatalf("GetQueueUrl: %v", err)
	}

	if aws.ToString(got.QueueUrl) != aws.ToString(out.QueueUrl) {
		t.Fatalf("URLs differ: create %q, get %q",
			aws.ToString(out.QueueUrl), aws.ToString(got.QueueUrl))
	}
}

func TestSDKSQSSendReceiveDeleteRoundTrip(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	q, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{
		QueueName: aws.String("loop"),
	})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	if _, err := client.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl:    q.QueueUrl,
		MessageBody: aws.String("hello world"),
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	rcv, err := client.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl:            q.QueueUrl,
		MaxNumberOfMessages: 1,
	})
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}

	if len(rcv.Messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(rcv.Messages))
	}

	if aws.ToString(rcv.Messages[0].Body) != "hello world" {
		t.Fatalf("Body = %q, want hello world", aws.ToString(rcv.Messages[0].Body))
	}

	if _, err := client.DeleteMessage(ctx, &awssqs.DeleteMessageInput{
		QueueUrl:      q.QueueUrl,
		ReceiptHandle: rcv.Messages[0].ReceiptHandle,
	}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
}

func TestSDKSQSListQueues(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	for _, n := range []string{"a", "b"} {
		if _, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{
			QueueName: aws.String(n),
		}); err != nil {
			t.Fatalf("create %s: %v", n, err)
		}
	}

	out, err := client.ListQueues(ctx, &awssqs.ListQueuesInput{})
	if err != nil {
		t.Fatalf("ListQueues: %v", err)
	}

	if len(out.QueueUrls) != 2 {
		t.Fatalf("listed %d, want 2", len(out.QueueUrls))
	}
}

func TestSDKSQSDeleteQueue(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	q, _ := client.CreateQueue(ctx, &awssqs.CreateQueueInput{
		QueueName: aws.String("doomed"),
	})

	if _, err := client.DeleteQueue(ctx, &awssqs.DeleteQueueInput{
		QueueUrl: q.QueueUrl,
	}); err != nil {
		t.Fatalf("DeleteQueue: %v", err)
	}

	_, err := client.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{
		QueueName: aws.String("doomed"),
	})
	if err == nil {
		t.Fatal("GetQueueUrl after delete returned nil error, want QueueDoesNotExist")
	}

	// The SDK only deserializes the typed QueueDoesNotExist error when the wire
	// __type is the modeled shape name "QueueDoesNotExist" — the shape the
	// Terraform / aws-sdk-go-v2 delete waiter matches (errs.IsA) to treat a queue
	// as gone. The legacy query code would drop back to a generic error here.
	var qdne *sqstypes.QueueDoesNotExist
	if !errors.As(err, &qdne) {
		t.Fatalf("GetQueueUrl error = %v, want typed *QueueDoesNotExist", err)
	}
}

func TestSDKSQSAddRemovePermission(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	q, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{
		QueueName: aws.String("perm-q"),
	})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	if _, err := client.AddPermission(ctx, &awssqs.AddPermissionInput{
		QueueUrl:      q.QueueUrl,
		Label:         aws.String("grant-send"),
		AWSAccountIds: []string{"123456789012"},
		Actions:       []string{"SendMessage"},
	}); err != nil {
		t.Fatalf("AddPermission: %v", err)
	}

	attrs, err := client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl:       q.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNamePolicy},
	})
	if err != nil {
		t.Fatalf("GetQueueAttributes: %v", err)
	}

	policy := attrs.Attributes["Policy"]
	if policy == "" {
		t.Fatal("Policy is empty after AddPermission")
	}

	for _, want := range []string{"grant-send", "SQS:SendMessage", "arn:aws:iam::123456789012:root"} {
		if !strings.Contains(policy, want) {
			t.Fatalf("Policy %q missing %q", policy, want)
		}
	}

	if _, err := client.RemovePermission(ctx, &awssqs.RemovePermissionInput{
		QueueUrl: q.QueueUrl,
		Label:    aws.String("grant-send"),
	}); err != nil {
		t.Fatalf("RemovePermission: %v", err)
	}

	attrs2, err := client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl:       q.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNamePolicy},
	})
	if err != nil {
		t.Fatalf("GetQueueAttributes after remove: %v", err)
	}

	if strings.Contains(attrs2.Attributes["Policy"], "grant-send") {
		t.Fatalf("Policy still contains grant-send after RemovePermission: %q", attrs2.Attributes["Policy"])
	}
}
