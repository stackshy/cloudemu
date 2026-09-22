package sqs_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func TestSDKCreateQueueRejectsBadNames(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	tests := []struct {
		name  string
		attrs map[string]string
	}{
		{name: "bad name with spaces!"},
		{name: strings.Repeat("a", 81)},
		{name: strings.Repeat("a", 76) + ".fifo", attrs: map[string]string{"FifoQueue": "true"}},
		// Real SQS does not infer FIFO from the suffix. Without FifoQueue=true
		// the dot makes this an invalid standard name.
		{name: "implied.fifo"},
	}

	for _, tc := range tests {
		_, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String(tc.name), Attributes: tc.attrs})
		if code := apiErrorCode(t, err); code != "InvalidParameterValue" {
			t.Errorf("CreateQueue(%q): code = %q, want InvalidParameterValue", tc.name, code)
		}
	}
}

func TestSDKSendMessageRejectsBadInput(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()
	url := mustCreateQueue(t, client, "send-validation")

	fifo, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{
		QueueName:  aws.String("send-validation.fifo"),
		Attributes: map[string]string{"FifoQueue": "true"},
	})
	if err != nil {
		t.Fatalf("CreateQueue fifo: %v", err)
	}

	tests := []struct {
		name  string
		input *awssqs.SendMessageInput
		want  string
	}{
		{
			name:  "empty body",
			input: &awssqs.SendMessageInput{QueueUrl: aws.String(url), MessageBody: aws.String("")},
			want:  "MissingParameter",
		},
		{
			name:  "control character",
			input: &awssqs.SendMessageInput{QueueUrl: aws.String(url), MessageBody: aws.String("bad\x01")},
			want:  "InvalidMessageContents",
		},
		{
			name: "bogus attribute type",
			input: &awssqs.SendMessageInput{
				QueueUrl: aws.String(url), MessageBody: aws.String("x"),
				MessageAttributes: map[string]types.MessageAttributeValue{
					"a": {DataType: aws.String("BogusType"), StringValue: aws.String("x")},
				},
			},
			want: "InvalidParameterValue",
		},
		{
			name: "per-message delay on fifo",
			input: &awssqs.SendMessageInput{
				QueueUrl: fifo.QueueUrl, MessageBody: aws.String("x"), DelaySeconds: 10,
				MessageGroupId: aws.String("g"), MessageDeduplicationId: aws.String("d"),
			},
			want: "InvalidParameterValue",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.SendMessage(ctx, tc.input)
			if code := apiErrorCode(t, err); code != tc.want {
				t.Fatalf("code = %q, want %s", code, tc.want)
			}
		})
	}

	// Valid input still goes through, including a labelled attribute type.
	_, err = client.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl: aws.String(url), MessageBody: aws.String("ok\t\n"),
		MessageAttributes: map[string]types.MessageAttributeValue{
			"a": {DataType: aws.String("String.custom"), StringValue: aws.String("x")},
		},
	})
	if err != nil {
		t.Fatalf("valid SendMessage: %v", err)
	}
}

func TestSDKSendMessageBatchEntryCodes(t *testing.T) {
	client, _ := newSDKClient(t)
	url := mustCreateQueue(t, client, "batch-entry-codes")

	out, err := client.SendMessageBatch(context.Background(), &awssqs.SendMessageBatchInput{
		QueueUrl: aws.String(url),
		Entries: []types.SendMessageBatchRequestEntry{
			{Id: aws.String("ok"), MessageBody: aws.String("fine")},
			{Id: aws.String("ctrl"), MessageBody: aws.String("bad\x01")},
		},
	})
	if err != nil {
		t.Fatalf("SendMessageBatch: %v", err)
	}

	if len(out.Successful) != 1 || len(out.Failed) != 1 {
		t.Fatalf("successful=%d failed=%d, want 1 and 1", len(out.Successful), len(out.Failed))
	}

	if got := aws.ToString(out.Failed[0].Code); got != "InvalidMessageContents" {
		t.Fatalf("failed entry code = %q, want InvalidMessageContents", got)
	}
}

func TestSDKReceiveMessageRejectsOutOfRange(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()
	url := mustCreateQueue(t, client, "recv-validation")

	tests := []struct {
		name  string
		input *awssqs.ReceiveMessageInput
	}{
		{name: "max 11", input: &awssqs.ReceiveMessageInput{QueueUrl: aws.String(url), MaxNumberOfMessages: 11}},
		{name: "max -1", input: &awssqs.ReceiveMessageInput{QueueUrl: aws.String(url), MaxNumberOfMessages: -1}},
		{name: "wait 21", input: &awssqs.ReceiveMessageInput{QueueUrl: aws.String(url), WaitTimeSeconds: 21}},
		{name: "visibility 43201", input: &awssqs.ReceiveMessageInput{QueueUrl: aws.String(url), VisibilityTimeout: 43201}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.ReceiveMessage(ctx, tc.input)
			if code := apiErrorCode(t, err); code != "InvalidParameterValue" {
				t.Fatalf("code = %q, want InvalidParameterValue", code)
			}
		})
	}
}

// The Go SDK drops zero-valued integers, but botocore and raw clients send
// them. An explicit MaxNumberOfMessages of 0 must be rejected, and an
// explicit VisibilityTimeout of 0 must keep the message visible.
func TestReceiveMessageExplicitZeroOnWire(t *testing.T) {
	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{SQS: cloud.SQS}))
	t.Cleanup(ts.Close)

	created := postSQS(t, ts.URL, "CreateQueue", `{"QueueName":"zero-wire"}`)
	url, _ := created.body["QueueUrl"].(string)

	postSQS(t, ts.URL, "SendMessage", `{"QueueUrl":"`+url+`","MessageBody":"x"}`)

	rejected := postSQS(t, ts.URL, "ReceiveMessage", `{"QueueUrl":"`+url+`","MaxNumberOfMessages":0}`)
	if rejected.status != http.StatusBadRequest || !strings.Contains(rejected.errType, "InvalidParameterValue") {
		t.Fatalf("explicit MaxNumberOfMessages=0: status=%d type=%q, want 400 InvalidParameterValue",
			rejected.status, rejected.errType)
	}

	first := postSQS(t, ts.URL, "ReceiveMessage", `{"QueueUrl":"`+url+`","VisibilityTimeout":0}`)
	if msgs, _ := first.body["Messages"].([]any); len(msgs) != 1 {
		t.Fatalf("first receive got %d messages, want 1", len(msgs))
	}

	again := postSQS(t, ts.URL, "ReceiveMessage", `{"QueueUrl":"`+url+`"}`)
	if msgs, _ := again.body["Messages"].([]any); len(msgs) != 1 {
		t.Fatalf("after VisibilityTimeout=0 the message should stay visible, got %d messages", len(msgs))
	}
}

// An explicit DelaySeconds of 0 on the wire overrides the queue delay. The Go
// SDK drops a zero value, so this test posts raw JSON.
func TestSendMessageExplicitZeroDelayOnWire(t *testing.T) {
	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{SQS: cloud.SQS}))
	t.Cleanup(ts.Close)

	created := postSQS(t, ts.URL, "CreateQueue", `{"QueueName":"delay-wire","Attributes":{"DelaySeconds":"60"}}`)
	url, _ := created.body["QueueUrl"].(string)

	postSQS(t, ts.URL, "SendMessage", `{"QueueUrl":"`+url+`","MessageBody":"now","DelaySeconds":0}`)
	postSQS(t, ts.URL, "SendMessage", `{"QueueUrl":"`+url+`","MessageBody":"later"}`)

	got := postSQS(t, ts.URL, "ReceiveMessage", `{"QueueUrl":"`+url+`","MaxNumberOfMessages":10}`)

	msgs, _ := got.body["Messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("got %d visible messages, want only the one sent with DelaySeconds=0", len(msgs))
	}

	if body, _ := msgs[0].(map[string]any)["Body"].(string); body != "now" {
		t.Fatalf("visible message body = %q, want now", body)
	}
}

func TestSDKFIFOMissingGroupAndDedupCodes(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	q, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{
		QueueName: aws.String("fifo-codes.fifo"), Attributes: map[string]string{"FifoQueue": "true"},
	})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	_, err = client.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl: q.QueueUrl, MessageBody: aws.String("x"), MessageDeduplicationId: aws.String("d"),
	})
	if code := apiErrorCode(t, err); code != "MissingParameter" {
		t.Errorf("no MessageGroupId: code = %q, want MissingParameter", code)
	}

	_, err = client.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl: q.QueueUrl, MessageBody: aws.String("x"), MessageGroupId: aws.String("g"),
	})
	if code := apiErrorCode(t, err); code != "InvalidParameterValue" {
		t.Errorf("no MessageDeduplicationId: code = %q, want InvalidParameterValue", code)
	}
}

type sqsReply struct {
	status  int
	errType string
	body    map[string]any
}

func postSQS(t *testing.T, base, op, payload string) sqsReply {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, base+"/", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS."+op)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s: %v", op, err)
	}
	defer resp.Body.Close()

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("%s: decode: %v", op, err)
	}

	errType, _ := body["__type"].(string)

	return sqsReply{status: resp.StatusCode, errType: errType, body: body}
}
