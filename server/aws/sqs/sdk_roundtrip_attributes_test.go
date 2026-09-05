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

// TestSDKSQSEncryptionAttributesRoundTrip confirms the server-side-encryption
// attributes (SqsManagedSseEnabled for SSE-SQS, and KmsMasterKeyId /
// KmsDataKeyReusePeriodSeconds for SSE-KMS) persist through CreateQueue and are
// echoed by GetQueueAttributes(All), matching real SQS. terraform-provider-aws
// reads these on every refresh, so a dropped attribute is perpetual drift.
func TestSDKSQSEncryptionAttributesRoundTrip(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	// SSE-SQS queue.
	sse, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{
		QueueName: aws.String("sse-sqs-q"),
		Attributes: map[string]string{
			string(sqstypes.QueueAttributeNameSqsManagedSseEnabled): "true",
		},
	})
	if err != nil {
		t.Fatalf("CreateQueue (SSE-SQS): %v", err)
	}

	got, err := client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl:       sse.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameAll},
	})
	if err != nil {
		t.Fatalf("GetQueueAttributes (SSE-SQS): %v", err)
	}

	if v := got.Attributes["SqsManagedSseEnabled"]; v != "true" {
		t.Fatalf("SqsManagedSseEnabled = %q, want true", v)
	}

	// SSE-KMS queue: KmsMasterKeyId set, so SqsManagedSseEnabled must read false
	// and KmsDataKeyReusePeriodSeconds must round-trip.
	kms, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{
		QueueName: aws.String("sse-kms-q"),
		Attributes: map[string]string{
			string(sqstypes.QueueAttributeNameKmsMasterKeyId):               "alias/aws/sqs",
			string(sqstypes.QueueAttributeNameKmsDataKeyReusePeriodSeconds): "600",
		},
	})
	if err != nil {
		t.Fatalf("CreateQueue (SSE-KMS): %v", err)
	}

	got, err = client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl:       kms.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameAll},
	})
	if err != nil {
		t.Fatalf("GetQueueAttributes (SSE-KMS): %v", err)
	}

	if v := got.Attributes["KmsMasterKeyId"]; v != "alias/aws/sqs" {
		t.Fatalf("KmsMasterKeyId = %q, want alias/aws/sqs", v)
	}

	if v := got.Attributes["KmsDataKeyReusePeriodSeconds"]; v != "600" {
		t.Fatalf("KmsDataKeyReusePeriodSeconds = %q, want 600", v)
	}

	if v := got.Attributes["SqsManagedSseEnabled"]; v != "false" {
		t.Fatalf("SqsManagedSseEnabled = %q, want false (mutually exclusive with SSE-KMS)", v)
	}
}

// TestSDKSQSFifoThroughputAttributesRoundTrip confirms the FIFO high-throughput
// attributes DeduplicationScope and FifoThroughputLimit persist through
// CreateQueue and are echoed only for FIFO queues, with the SQS defaults
// (queue / perQueue) applied when unset. Matches real SQS.
func TestSDKSQSFifoThroughputAttributesRoundTrip(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	ht, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{
		QueueName: aws.String("ht-q.fifo"),
		Attributes: map[string]string{
			string(sqstypes.QueueAttributeNameFifoQueue):           "true",
			string(sqstypes.QueueAttributeNameDeduplicationScope):  "messageGroup",
			string(sqstypes.QueueAttributeNameFifoThroughputLimit): "perMessageGroupId",
		},
	})
	if err != nil {
		t.Fatalf("CreateQueue (HT FIFO): %v", err)
	}

	got, err := client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl:       ht.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameAll},
	})
	if err != nil {
		t.Fatalf("GetQueueAttributes (HT FIFO): %v", err)
	}

	if v := got.Attributes["DeduplicationScope"]; v != "messageGroup" {
		t.Fatalf("DeduplicationScope = %q, want messageGroup", v)
	}

	if v := got.Attributes["FifoThroughputLimit"]; v != "perMessageGroupId" {
		t.Fatalf("FifoThroughputLimit = %q, want perMessageGroupId", v)
	}

	// A default FIFO queue reports the SQS defaults.
	def, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{
		QueueName: aws.String("def-q.fifo"),
		Attributes: map[string]string{
			string(sqstypes.QueueAttributeNameFifoQueue): "true",
		},
	})
	if err != nil {
		t.Fatalf("CreateQueue (default FIFO): %v", err)
	}

	got, err = client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl:       def.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameAll},
	})
	if err != nil {
		t.Fatalf("GetQueueAttributes (default FIFO): %v", err)
	}

	if v := got.Attributes["DeduplicationScope"]; v != "queue" {
		t.Fatalf("default DeduplicationScope = %q, want queue", v)
	}

	if v := got.Attributes["FifoThroughputLimit"]; v != "perQueue" {
		t.Fatalf("default FifoThroughputLimit = %q, want perQueue", v)
	}

	// A standard queue must not report the FIFO-only attributes.
	std, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("std-noht-q")})
	if err != nil {
		t.Fatalf("CreateQueue (standard): %v", err)
	}

	got, err = client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl:       std.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameAll},
	})
	if err != nil {
		t.Fatalf("GetQueueAttributes (standard): %v", err)
	}

	if _, ok := got.Attributes["DeduplicationScope"]; ok {
		t.Fatal("standard queue must not report DeduplicationScope")
	}

	if _, ok := got.Attributes["FifoThroughputLimit"]; ok {
		t.Fatal("standard queue must not report FifoThroughputLimit")
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

// TestSDKSQSSendMessageBatchTraceHeader confirms SendMessageBatch accepts the
// AWSTraceHeader message system attribute per entry, returns
// MD5OfMessageSystemAttributes per successful entry, and that ReceiveMessage
// surfaces AWSTraceHeader in the message Attributes map. Matches real SQS.
func TestSDKSQSSendMessageBatchTraceHeader(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	q, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("batch-trace-q")})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	const trace = "Root=1-5759e988-bd862e3fe1be46a994272793;Parent=53995c3f42cd8ad8;Sampled=1"

	out, err := client.SendMessageBatch(ctx, &awssqs.SendMessageBatchInput{
		QueueUrl: q.QueueUrl,
		Entries: []sqstypes.SendMessageBatchRequestEntry{
			{
				Id:          aws.String("m1"),
				MessageBody: aws.String("traced-batch"),
				MessageSystemAttributes: map[string]sqstypes.MessageSystemAttributeValue{
					string(sqstypes.MessageSystemAttributeNameForSendsAWSTraceHeader): {
						DataType:    aws.String("String"),
						StringValue: aws.String(trace),
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("SendMessageBatch: %v", err)
	}

	if len(out.Successful) != 1 {
		t.Fatalf("got %d successful, want 1 (failed=%d)", len(out.Successful), len(out.Failed))
	}

	if got := aws.ToString(out.Successful[0].MD5OfMessageSystemAttributes); got != wantTraceMD5(trace) {
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

// TestSDKSQSSystemAttributeInvalidKeyRejected confirms that a message system
// attribute whose key is not AWSTraceHeader is rejected with a 4xx
// InvalidParameterValue error on both SendMessage and SendMessageBatch, matching
// real SQS (the only valid system attribute key is AWSTraceHeader).
func TestSDKSQSSystemAttributeInvalidKeyRejected(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	q, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("bad-sysattr-q")})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	badAttrs := map[string]sqstypes.MessageSystemAttributeValue{
		"NotATraceHeader": {
			DataType:    aws.String("String"),
			StringValue: aws.String("nope"),
		},
	}

	if _, err := client.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl:                q.QueueUrl,
		MessageBody:             aws.String("body"),
		MessageSystemAttributes: badAttrs,
	}); err == nil {
		t.Fatal("SendMessage with invalid system attribute key succeeded, want error")
	}

	if _, err := client.SendMessageBatch(ctx, &awssqs.SendMessageBatchInput{
		QueueUrl: q.QueueUrl,
		Entries: []sqstypes.SendMessageBatchRequestEntry{
			{
				Id:                      aws.String("m1"),
				MessageBody:             aws.String("body"),
				MessageSystemAttributes: badAttrs,
			},
		},
	}); err == nil {
		t.Fatal("SendMessageBatch with invalid system attribute key succeeded, want error")
	}
}
