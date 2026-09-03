package lambda_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/stackshy/cloudemu/v2"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newLambdaSQSClients boots a server serving both the Lambda and SQS wire APIs
// against one shared AWS cloud, so a Lambda DeadLetterConfig pointed at an SQS
// queue actually delivers there (cloudemu.NewAWS already wires
// Lambda.SetAsyncDestinationTargets(SQS, SNS)).
func newLambdaSQSClients(t *testing.T) (*awslambda.Client, *awssqs.Client, *awsprovider.Provider) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{Lambda: cloud.Lambda, SQS: cloud.SQS})

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

	lc := awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	sc := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	return lc, sc, cloud
}

// TestSDKAsyncFailureDeliversToDLQ is the real-user end-to-end flow: a function
// configured to fail, with a DeadLetterConfig pointed at a real SQS queue, is
// invoked asynchronously (InvocationType=Event) via the aws-sdk-go-v2 client;
// after retries the failed event lands in the SQS DLQ (observed via
// ReceiveMessage). An OnFailure destination configured with
// PutFunctionEventInvokeConfig receives the error envelope too.
func TestSDKAsyncFailureDeliversToDLQ(t *testing.T) {
	lc, sc, cloud := newLambdaSQSClients(t)
	ctx := context.Background()

	// A DLQ queue and a separate OnFailure destination queue.
	dlqARN := createQueueARN(t, sc, "dlq")
	failARN := createQueueARN(t, sc, "on-failure")

	if _, err := lc.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName:     aws.String("worker"),
		Runtime:          lambdatypes.RuntimeGo1x,
		Role:             aws.String("arn:aws:iam::123456789012:role/test"),
		Handler:          aws.String("main"),
		Code:             &lambdatypes.FunctionCode{ZipFile: []byte("z")},
		DeadLetterConfig: &lambdatypes.DeadLetterConfig{TargetArn: aws.String(dlqARN)},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	// OnFailure destination via PutFunctionEventInvokeConfig.
	if _, err := lc.PutFunctionEventInvokeConfig(ctx, &awslambda.PutFunctionEventInvokeConfigInput{
		FunctionName:         aws.String("worker"),
		MaximumRetryAttempts: aws.Int32(1),
		DestinationConfig: &lambdatypes.DestinationConfig{
			OnFailure: &lambdatypes.OnFailure{Destination: aws.String(failARN)},
		},
	}); err != nil {
		t.Fatalf("PutFunctionEventInvokeConfig: %v", err)
	}

	// Round-trip the config through the SDK.
	got, err := lc.GetFunctionEventInvokeConfig(ctx, &awslambda.GetFunctionEventInvokeConfigInput{
		FunctionName: aws.String("worker"),
	})
	if err != nil {
		t.Fatalf("GetFunctionEventInvokeConfig: %v", err)
	}
	if got.MaximumRetryAttempts == nil || *got.MaximumRetryAttempts != 1 {
		t.Fatalf("MaximumRetryAttempts = %v, want 1", got.MaximumRetryAttempts)
	}
	if got.DestinationConfig == nil || got.DestinationConfig.OnFailure == nil {
		t.Fatal("GetFunctionEventInvokeConfig dropped OnFailure destination")
	}

	// Make the function fail. The wire has no runtime; register a failing Go
	// handler on the shared cloud (the same seam the sync-invoke SDK test uses).
	cloud.Lambda.RegisterHandler("worker", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, errors.New("kaboom")
	})

	// Async invoke: fire-and-forget, HTTP 202.
	resp, err := lc.Invoke(ctx, &awslambda.InvokeInput{
		FunctionName:   aws.String("worker"),
		InvocationType: lambdatypes.InvocationTypeEvent,
		Payload:        []byte(`{"order":42}`),
	})
	if err != nil {
		t.Fatalf("Invoke(Event): %v", err)
	}
	if resp.StatusCode != 202 {
		t.Fatalf("StatusCode = %d, want 202", resp.StatusCode)
	}

	// The failed event must now be in the SQS DLQ.
	dlqBody := receiveOne(t, sc, "dlq")
	if dlqBody != `{"order":42}` {
		t.Fatalf("DLQ message = %q, want the original event", dlqBody)
	}

	// The OnFailure destination gets the async-destination envelope with error.
	failBody := receiveOne(t, sc, "on-failure")
	if !strings.Contains(failBody, "RetriesExhausted") || !strings.Contains(failBody, "kaboom") {
		t.Fatalf("OnFailure envelope = %q, want RetriesExhausted + error", failBody)
	}
}

// TestSDKAsyncSuccessNoDLQ guards that a successful async invoke does not deliver
// to the DLQ.
func TestSDKAsyncSuccessNoDLQ(t *testing.T) {
	lc, sc, cloud := newLambdaSQSClients(t)
	ctx := context.Background()

	dlqARN := createQueueARN(t, sc, "dlq")

	if _, err := lc.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName:     aws.String("ok"),
		Runtime:          lambdatypes.RuntimeGo1x,
		Role:             aws.String("arn:aws:iam::123456789012:role/test"),
		Handler:          aws.String("main"),
		Code:             &lambdatypes.FunctionCode{ZipFile: []byte("z")},
		DeadLetterConfig: &lambdatypes.DeadLetterConfig{TargetArn: aws.String(dlqARN)},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	cloud.Lambda.RegisterHandler("ok", func(_ context.Context, p []byte) ([]byte, error) {
		return p, nil
	})

	if _, err := lc.Invoke(ctx, &awslambda.InvokeInput{
		FunctionName:   aws.String("ok"),
		InvocationType: lambdatypes.InvocationTypeEvent,
		Payload:        []byte(`{"n":1}`),
	}); err != nil {
		t.Fatalf("Invoke(Event): %v", err)
	}

	// Give any (erroneous) delivery a chance; then assert the DLQ is empty.
	msgs, err := sc.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl:            aws.String(queueURL(t, sc, "dlq")),
		MaxNumberOfMessages: 1,
		WaitTimeSeconds:     0,
	})
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}
	if len(msgs.Messages) != 0 {
		t.Fatalf("DLQ has %d messages on success, want 0", len(msgs.Messages))
	}
}

func createQueueARN(t *testing.T, sc *awssqs.Client, name string) string {
	t.Helper()

	if _, err := sc.CreateQueue(context.Background(), &awssqs.CreateQueueInput{
		QueueName: aws.String(name),
	}); err != nil {
		t.Fatalf("CreateQueue(%s): %v", name, err)
	}

	attrs, err := sc.GetQueueAttributes(context.Background(), &awssqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(queueURL(t, sc, name)),
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})
	if err != nil {
		t.Fatalf("GetQueueAttributes(%s): %v", name, err)
	}

	return attrs.Attributes[string(sqstypes.QueueAttributeNameQueueArn)]
}

func queueURL(t *testing.T, sc *awssqs.Client, name string) string {
	t.Helper()

	out, err := sc.GetQueueUrl(context.Background(), &awssqs.GetQueueUrlInput{QueueName: aws.String(name)})
	if err != nil {
		t.Fatalf("GetQueueUrl(%s): %v", name, err)
	}

	return *out.QueueUrl
}

// receiveOne polls the named queue for a single message body, failing if none
// arrives.
func receiveOne(t *testing.T, sc *awssqs.Client, name string) string {
	t.Helper()

	url := queueURL(t, sc, name)

	for i := 0; i < 20; i++ {
		out, err := sc.ReceiveMessage(context.Background(), &awssqs.ReceiveMessageInput{
			QueueUrl:            aws.String(url),
			MaxNumberOfMessages: 1,
		})
		if err != nil {
			t.Fatalf("ReceiveMessage(%s): %v", name, err)
		}

		if len(out.Messages) > 0 {
			return *out.Messages[0].Body
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("no message arrived in queue %s", name)

	return ""
}
