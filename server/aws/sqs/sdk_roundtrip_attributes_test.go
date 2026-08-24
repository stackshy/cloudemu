package sqs_test

import (
	"context"
	"crypto/md5" //nolint:gosec // SQS MD5 checksums are part of the wire protocol, not security.
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// wantTraceMD5 independently computes the AWS-documented MD5 digest over a single
// String-typed AWSTraceHeader system attribute, verifying the server's digest.
func wantTraceMD5(value string) string {
	h := md5.New() //nolint:gosec // wire checksum

	write := func(b []byte) {
		var prefix [4]byte

		binary.BigEndian.PutUint32(prefix[:], uint32(len(b)))
		_, _ = h.Write(prefix[:])
		_, _ = h.Write(b)
	}

	write([]byte("AWSTraceHeader"))
	write([]byte("String"))
	_, _ = h.Write([]byte{1})
	write([]byte(value))

	return hex.EncodeToString(h.Sum(nil))
}

// TestSDKSQSRedriveAllowPolicyRoundTrip confirms the RedriveAllowPolicy queue
// attribute persists through SetQueueAttributes and is echoed by
// GetQueueAttributes(All), matching real SQS.
func TestSDKSQSRedriveAllowPolicyRoundTrip(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	q, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("rap-q")})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	const policy = `{"redrivePermission":"byQueue","sourceQueueArns":["arn:aws:sqs:us-east-1:000000000000:src"]}`

	if _, err := client.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{
		QueueUrl: q.QueueUrl,
		Attributes: map[string]string{
			string(sqstypes.QueueAttributeNameRedriveAllowPolicy): policy,
		},
	}); err != nil {
		t.Fatalf("SetQueueAttributes: %v", err)
	}

	got, err := client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl:       q.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameAll},
	})
	if err != nil {
		t.Fatalf("GetQueueAttributes: %v", err)
	}

	if v := got.Attributes["RedriveAllowPolicy"]; v != policy {
		t.Fatalf("RedriveAllowPolicy = %q, want %q", v, policy)
	}
}

// TestSDKSQSRedriveAllowPolicyOnCreate confirms RedriveAllowPolicy supplied at
// CreateQueue time is persisted and returned by GetQueueAttributes.
func TestSDKSQSRedriveAllowPolicyOnCreate(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	const policy = `{"redrivePermission":"denyAll"}`

	q, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{
		QueueName: aws.String("rap-create-q"),
		Attributes: map[string]string{
			string(sqstypes.QueueAttributeNameRedriveAllowPolicy): policy,
		},
	})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	got, err := client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl:       q.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameRedriveAllowPolicy},
	})
	if err != nil {
		t.Fatalf("GetQueueAttributes: %v", err)
	}

	if v := got.Attributes["RedriveAllowPolicy"]; v != policy {
		t.Fatalf("RedriveAllowPolicy = %q, want %q", v, policy)
	}
}

// TestSDKSQSAWSTraceHeaderSystemAttribute confirms SendMessage accepts the
// AWSTraceHeader message system attribute, returns MD5OfMessageSystemAttributes,
// and ReceiveMessage returns AWSTraceHeader in the message Attributes map when
// requested. Matches real SQS.
func TestSDKSQSAWSTraceHeaderSystemAttribute(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	q, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("trace-q")})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	const trace = "Root=1-5759e988-bd862e3fe1be46a994272793;Parent=53995c3f42cd8ad8;Sampled=1"

	send, err := client.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl:    q.QueueUrl,
		MessageBody: aws.String("traced"),
		MessageSystemAttributes: map[string]sqstypes.MessageSystemAttributeValue{
			string(sqstypes.MessageSystemAttributeNameForSendsAWSTraceHeader): {
				DataType:    aws.String("String"),
				StringValue: aws.String(trace),
			},
		},
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	if aws.ToString(send.MD5OfMessageSystemAttributes) == "" {
		t.Fatal("MD5OfMessageSystemAttributes is empty, want a digest")
	}

	// AWS-documented canonical MD5 of {AWSTraceHeader: String=trace}.
	if got := aws.ToString(send.MD5OfMessageSystemAttributes); got != wantTraceMD5(trace) {
		t.Fatalf("MD5OfMessageSystemAttributes = %q, want %q", got, wantTraceMD5(trace))
	}

	rcv, err := client.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl:            q.QueueUrl,
		MaxNumberOfMessages: 1,
		MessageSystemAttributeNames: []sqstypes.MessageSystemAttributeName{
			sqstypes.MessageSystemAttributeNameAWSTraceHeader,
		},
	})
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}

	if len(rcv.Messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(rcv.Messages))
	}

	if v := rcv.Messages[0].Attributes["AWSTraceHeader"]; v != trace {
		t.Fatalf("AWSTraceHeader = %q, want %q", v, trace)
	}
}
