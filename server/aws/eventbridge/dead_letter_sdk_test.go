// dead_letter_sdk_test.go — real aws-sdk-go-v2 end-to-end test verifying that a
// target's configured DeadLetterConfig receives the original event when
// dispatch to the target itself fails (a deleted queue behind a stale target
// ARN, or a Lambda handler that raises). Real EventBridge routes a failed
// invocation to the target's DLQ rather than silently dropping it.
package eventbridge_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/stackshy/cloudemu/v2"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

const dlqPollTimeout = 2 * time.Second

var errSDKHandlerFailed = errors.New("handler failed")

// dlqClients wires one wire server serving EventBridge, SQS, and Lambda from a
// single cloud (so cross-service DLQ delivery is exercised for real).
type dlqClients struct {
	eb  *awseb.Client
	sqs *awssqs.Client
	lam *awslambda.Client
}

func newDLQClients(t *testing.T, cloud *awsprovider.Provider) dlqClients {
	t.Helper()

	srv := awsserver.New(awsserver.Drivers{
		EventBridge: cloud.EventBridge,
		SQS:         cloud.SQS,
		Lambda:      cloud.Lambda,
		CloudWatch:  cloud.CloudWatch,
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return dlqClients{
		eb:  awseb.NewFromConfig(cfg, func(o *awseb.Options) { o.BaseEndpoint = aws.String(ts.URL) }),
		sqs: awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) }),
		lam: awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(ts.URL) }),
	}
}

func dlqQueue(t *testing.T, sqs *awssqs.Client, name string) (url, arn string) {
	t.Helper()
	return makeQueue(t, sqs, name)
}

// pollForMessage retries ReceiveMessage until a message arrives or the timeout
// elapses, since delivery happens on a goroutine decoupled from PutEvents.
func pollForMessage(t *testing.T, sqs *awssqs.Client, url string) string {
	t.Helper()

	deadline := time.Now().Add(dlqPollTimeout)
	for time.Now().Before(deadline) {
		if body, count := receiveOne(t, sqs, url); count > 0 {
			return body
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("no message received on %s within %s", url, dlqPollTimeout)

	return ""
}

func TestSDKEventBridgeDeadLetterOnStaleSQSTarget(t *testing.T) {
	cloud := cloudemu.NewAWS()
	c := newDLQClients(t, cloud)
	ctx := context.Background()

	targetURL, targetARN := dlqQueue(t, c.sqs, "eb-stale-target")
	dlqURL, dlqARN := dlqQueue(t, c.sqs, "eb-dlq")

	if _, err := c.eb.PutRule(ctx, &awseb.PutRuleInput{
		Name:         aws.String("r"),
		EventPattern: aws.String(`{"source":["myapp"]}`),
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if _, err := c.eb.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule: aws.String("r"),
		Targets: []ebtypes.Target{{
			Id:               aws.String("1"),
			Arn:              aws.String(targetARN),
			DeadLetterConfig: &ebtypes.DeadLetterConfig{Arn: aws.String(dlqARN)},
		}},
	}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	// The target queue goes stale after PutTargets.
	if _, err := c.sqs.DeleteQueue(ctx, &awssqs.DeleteQueueInput{QueueUrl: aws.String(targetURL)}); err != nil {
		t.Fatalf("DeleteQueue: %v", err)
	}

	if _, err := c.eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("myapp"), DetailType: aws.String("t"), Detail: aws.String(`{"orderId":"42"}`)},
	}}); err != nil {
		t.Fatalf("PutEvents: %v", err)
	}

	body := pollForMessage(t, c.sqs, dlqURL)

	var env map[string]any
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("DLQ body not JSON: %v (%s)", err, body)
	}

	if env["source"] != "myapp" {
		t.Fatalf("unexpected DLQ envelope: %+v", env)
	}
}

func TestSDKEventBridgeDeadLetterOnFailingLambdaTarget(t *testing.T) {
	cloud := cloudemu.NewAWS()
	cloud.Lambda.RegisterHandler("eb-failing-fn", func(context.Context, []byte) ([]byte, error) {
		return nil, errSDKHandlerFailed
	})

	c := newDLQClients(t, cloud)
	ctx := context.Background()

	fnOut, err := c.lam.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("eb-failing-fn"),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("stub")},
	})
	if err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	dlqURL, dlqARN := dlqQueue(t, c.sqs, "eb-dlq")

	if _, err := c.eb.PutRule(ctx, &awseb.PutRuleInput{
		Name:         aws.String("r"),
		EventPattern: aws.String(`{"source":["myapp"]}`),
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if _, err := c.eb.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule: aws.String("r"),
		Targets: []ebtypes.Target{{
			Id:               aws.String("1"),
			Arn:              fnOut.FunctionArn,
			DeadLetterConfig: &ebtypes.DeadLetterConfig{Arn: aws.String(dlqARN)},
		}},
	}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	if _, err := c.eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("myapp"), DetailType: aws.String("t"), Detail: aws.String(`{"orderId":"42"}`)},
	}}); err != nil {
		t.Fatalf("PutEvents: %v", err)
	}

	pollForMessage(t, c.sqs, dlqURL)
}
