// sqs_esm_sdk_test.go — real aws-sdk-go-v2 end-to-end test for the SQS ->
// Lambda event-source-mapping delivery path. Creating a mapping from a queue
// to a function, then sending a message, must synchronously invoke the mapped
// function with a real SQS event batch and delete the message on success
// (previously CreateEventSourceMapping only stored config and nothing ever
// invoked the function, nor deleted the message, leaving the DLQ redrive path
// unreachable). Mirrors dynamodb_stream_esm_sdk_test.go for the SQS source.
package lambda_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestSDKSQSESMInvokesLambdaAndDeletesMessage verifies that sending a message
// to a queue with an enabled event-source-mapping synchronously invokes the
// mapped Lambda with the documented SQS event shape, and deletes the message
// from the queue once the handler succeeds.
func TestSDKSQSESMInvokesLambdaAndDeletesMessage(t *testing.T) {
	cloud := cloudemu.NewAWS()

	invocations := make(chan []byte, 4)
	cloud.Lambda.RegisterHandler("sqs-processor", func(_ context.Context, payload []byte) ([]byte, error) {
		invocations <- payload
		return payload, nil
	})

	srv := awsserver.New(awsserver.Drivers{
		SQS:        cloud.SQS,
		Lambda:     cloud.Lambda,
		CloudWatch: cloud.CloudWatch,
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	sqsClient, lam := newSQSAndLambda(t, ts.URL)
	ctx := context.Background()

	queueURL, queueARN := createQueue(t, sqsClient, "orders-queue")
	createProcessorFunction(t, lam, "sqs-processor")

	esm, err := lam.CreateEventSourceMapping(ctx, &awslambda.CreateEventSourceMappingInput{
		EventSourceArn: aws.String(queueARN),
		FunctionName:   aws.String("sqs-processor"),
		Enabled:        aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("CreateEventSourceMapping: %v", err)
	}

	if esm.State == nil || *esm.State != "Enabled" {
		t.Fatalf("mapping State = %v, want Enabled", esm.State)
	}

	// Sending a message must invoke the mapped Lambda.
	if _, err := sqsClient.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl:    aws.String(queueURL),
		MessageBody: aws.String("New order!"),
		MessageAttributes: map[string]sqstypes.MessageAttributeValue{
			"orderType": {DataType: aws.String("String"), StringValue: aws.String("standard")},
		},
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	payload := awaitInvocation(t, invocations)
	assertSQSEvent(t, payload, queueARN)

	// The handler succeeded, so the message must have been deleted -- a
	// receive against the (now empty) queue finds nothing.
	rcv, err := sqsClient.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl:            aws.String(queueURL),
		MaxNumberOfMessages: 10,
	})
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}

	if len(rcv.Messages) != 0 {
		t.Fatalf("queue has %d messages after successful processing, want 0", len(rcv.Messages))
	}
}

// TestSDKSQSESMFailureRedrivesToDLQ verifies that a message whose mapped
// Lambda handler always fails is redriven to the RedrivePolicy's dead-letter
// queue once its receive count exceeds MaxReceiveCount, exercising the
// existing DLQ-redrive logic end to end through Lambda ESM delivery.
func TestSDKSQSESMFailureRedrivesToDLQ(t *testing.T) {
	cloud := cloudemu.NewAWS()

	var invocationCount int
	invoked := make(chan struct{}, 8)
	cloud.Lambda.RegisterHandler("always-fails", func(_ context.Context, _ []byte) ([]byte, error) {
		invocationCount++
		invoked <- struct{}{}
		return nil, fmt.Errorf("processing failed")
	})

	srv := awsserver.New(awsserver.Drivers{
		SQS:        cloud.SQS,
		Lambda:     cloud.Lambda,
		CloudWatch: cloud.CloudWatch,
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	sqsClient, lam := newSQSAndLambda(t, ts.URL)
	ctx := context.Background()

	dlqURL, dlqARN := createQueue(t, sqsClient, "poison-dlq")
	mainURL, mainARN := createQueue(t, sqsClient, "poison-main")

	redrive := fmt.Sprintf(`{"deadLetterTargetArn":%q,"maxReceiveCount":2}`, dlqARN)
	if _, err := sqsClient.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{
		QueueUrl:   aws.String(mainURL),
		Attributes: map[string]string{"RedrivePolicy": redrive},
	}); err != nil {
		t.Fatalf("SetQueueAttributes: %v", err)
	}

	createProcessorFunction(t, lam, "always-fails")

	if _, err := lam.CreateEventSourceMapping(ctx, &awslambda.CreateEventSourceMappingInput{
		EventSourceArn: aws.String(mainARN),
		FunctionName:   aws.String("always-fails"),
		Enabled:        aws.Bool(true),
	}); err != nil {
		t.Fatalf("CreateEventSourceMapping: %v", err)
	}

	if _, err := sqsClient.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl:    aws.String(mainURL),
		MessageBody: aws.String("poison"),
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	for i := 0; i < 2; i++ {
		select {
		case <-invoked:
		case <-time.After(2 * time.Second):
			t.Fatalf("handler was invoked %d times, want 2", i)
		}
	}

	if invocationCount != 2 {
		t.Fatalf("invocationCount = %d, want exactly 2 (MaxReceiveCount)", invocationCount)
	}

	// The main queue must now be empty...
	mainRcv, err := sqsClient.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl:            aws.String(mainURL),
		MaxNumberOfMessages: 10,
	})
	if err != nil {
		t.Fatalf("ReceiveMessage(main): %v", err)
	}

	if len(mainRcv.Messages) != 0 {
		t.Fatalf("main queue has %d messages after redrive, want 0", len(mainRcv.Messages))
	}

	// ...and the DLQ must hold exactly the redriven message.
	dlqRcv, err := sqsClient.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl:            aws.String(dlqURL),
		MaxNumberOfMessages: 10,
	})
	if err != nil {
		t.Fatalf("ReceiveMessage(dlq): %v", err)
	}

	if len(dlqRcv.Messages) != 1 {
		t.Fatalf("DLQ has %d messages, want 1", len(dlqRcv.Messages))
	}

	if aws.ToString(dlqRcv.Messages[0].Body) != "poison" {
		t.Errorf("DLQ body = %q, want poison", aws.ToString(dlqRcv.Messages[0].Body))
	}
}

// assertSQSEvent verifies the payload Lambda received is the documented SQS
// event shape: https://docs.aws.amazon.com/lambda/latest/dg/with-sqs.html.
func assertSQSEvent(t *testing.T, payload []byte, queueARN string) {
	t.Helper()

	var event struct {
		Records []struct {
			MessageID         string            `json:"messageId"`
			ReceiptHandle     string            `json:"receiptHandle"`
			Body              string            `json:"body"`
			Attributes        map[string]string `json:"attributes"`
			MessageAttributes map[string]struct {
				StringValue string `json:"stringValue"`
				DataType    string `json:"dataType"`
			} `json:"messageAttributes"`
			MD5OfBody      string `json:"md5OfBody"`
			EventSource    string `json:"eventSource"`
			EventSourceARN string `json:"eventSourceARN"`
			AWSRegion      string `json:"awsRegion"`
		} `json:"Records"`
	}

	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("unmarshal SQS event: %v\npayload=%s", err, payload)
	}

	if len(event.Records) != 1 {
		t.Fatalf("Records = %d, want 1\npayload=%s", len(event.Records), payload)
	}

	rec := event.Records[0]

	if rec.Body != "New order!" {
		t.Fatalf("body = %q, want %q", rec.Body, "New order!")
	}

	if rec.ReceiptHandle == "" || rec.MessageID == "" || rec.MD5OfBody == "" {
		t.Fatalf("record missing messageId/receiptHandle/md5OfBody: %+v", rec)
	}

	if rec.EventSource != "aws:sqs" {
		t.Fatalf("eventSource = %q, want aws:sqs", rec.EventSource)
	}

	if rec.EventSourceARN != queueARN {
		t.Fatalf("eventSourceARN = %q, want %q", rec.EventSourceARN, queueARN)
	}

	if rec.AWSRegion == "" {
		t.Fatal("awsRegion is empty")
	}

	if rec.Attributes["ApproximateReceiveCount"] != "1" {
		t.Fatalf("ApproximateReceiveCount = %q, want 1", rec.Attributes["ApproximateReceiveCount"])
	}

	if rec.Attributes["SentTimestamp"] == "" || rec.Attributes["SenderId"] == "" ||
		rec.Attributes["ApproximateFirstReceiveTimestamp"] == "" {
		t.Fatalf("attributes missing standard fields: %+v", rec.Attributes)
	}

	if rec.MessageAttributes["orderType"].StringValue != "standard" ||
		rec.MessageAttributes["orderType"].DataType != "String" {
		t.Fatalf("messageAttributes = %+v", rec.MessageAttributes)
	}
}

// createQueue creates a standard SQS queue and returns its URL and ARN.
func createQueue(t *testing.T, client *awssqs.Client, name string) (queueURL, queueARN string) {
	t.Helper()

	ctx := context.Background()

	out, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String(name)})
	if err != nil {
		t.Fatalf("CreateQueue(%s): %v", name, err)
	}

	attrs, err := client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl:       out.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})
	if err != nil {
		t.Fatalf("GetQueueAttributes(%s): %v", name, err)
	}

	arn := attrs.Attributes["QueueArn"]
	if arn == "" {
		t.Fatalf("GetQueueAttributes(%s) returned an empty QueueArn", name)
	}

	return aws.ToString(out.QueueUrl), arn
}

func newSQSAndLambda(t *testing.T, url string) (*awssqs.Client, *awslambda.Client) {
	t.Helper()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	sqsClient := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(url) })
	lam := awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(url) })

	return sqsClient, lam
}
