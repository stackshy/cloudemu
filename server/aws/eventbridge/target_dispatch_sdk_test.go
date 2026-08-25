// target_dispatch_sdk_test.go — real aws-sdk-go-v2 end-to-end test for
// EventBridge target dispatch by ARN service. A matching PutEvents must reach
// every first-class target type, not only SQS: a Lambda function target is
// invoked (ASYNC) and an SNS topic target is published (fanning out to its
// SQS-protocol subscription). Previously deliverToTargets hard-coded a single
// SQS-only branch, so Lambda/SNS/Step Functions targets were accepted by
// PutTargets but silently never delivered.
package eventbridge_test

import (
	"context"
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
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

type ebTargetClients struct {
	eb  *awseb.Client
	sqs *awssqs.Client
	sns *awssns.Client
	lam *awslambda.Client
}

// newEBTargets wires one wire server serving EventBridge, SQS, SNS and Lambda
// from a single cloud (so cross-service target delivery is exercised for real),
// registering a Lambda handler that reports each invocation on the returned
// channel.
func newEBTargets(t *testing.T, funcName string) (ebTargetClients, <-chan []byte) {
	t.Helper()

	cloud := cloudemu.NewAWS()

	invocations := make(chan []byte, 4)
	cloud.Lambda.RegisterHandler(funcName, func(_ context.Context, payload []byte) ([]byte, error) {
		invocations <- payload

		return payload, nil
	})

	srv := awsserver.New(awsserver.Drivers{
		EventBridge: cloud.EventBridge,
		SQS:         cloud.SQS,
		SNS:         cloud.SNS,
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

	return ebTargetClients{
		eb:  awseb.NewFromConfig(cfg, func(o *awseb.Options) { o.BaseEndpoint = aws.String(ts.URL) }),
		sqs: awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) }),
		sns: awssns.NewFromConfig(cfg, func(o *awssns.Options) { o.BaseEndpoint = aws.String(ts.URL) }),
		lam: awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(ts.URL) }),
	}, invocations
}

// snsTopicToQueue creates an SNS topic subscribed (raw delivery) to a fresh SQS
// queue and returns the topic ARN plus the queue URL, so an SNS publish is
// observable as a plain message on the queue.
func snsTopicToQueue(t *testing.T, c ebTargetClients, topicName, queueName string) (topicARN, queueURL string) {
	t.Helper()

	ctx := context.Background()

	queueURL, queueARN := makeQueue(t, c.sqs, queueName)

	topic, err := c.sns.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String(topicName)})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	if _, err := c.sns.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn:              topic.TopicArn,
		Protocol:              aws.String("sqs"),
		Endpoint:              aws.String(queueARN),
		ReturnSubscriptionArn: true,
		Attributes:            map[string]string{"RawMessageDelivery": "true"},
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	return aws.ToString(topic.TopicArn), queueURL
}

func createLambda(t *testing.T, lam *awslambda.Client, name string) string {
	t.Helper()

	out, err := lam.CreateFunction(context.Background(), &awslambda.CreateFunctionInput{
		FunctionName: aws.String(name),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("stub")},
	})
	if err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	return aws.ToString(out.FunctionArn)
}

// TestSDKEventBridgeDeliversToLambdaAndSNS verifies that a single rule with a
// Lambda target and an SNS-topic target delivers a matching event to BOTH: the
// Lambda handler is invoked and SNS fans the event out to its SQS subscription.
// A non-matching event fires neither.
func TestSDKEventBridgeDeliversToLambdaAndSNS(t *testing.T) {
	c, invocations := newEBTargets(t, "eb-fn")
	ctx := context.Background()

	fnARN := createLambda(t, c.lam, "eb-fn")
	topicARN, queueURL := snsTopicToQueue(t, c, "eb-topic", "eb-sns-queue")

	if _, err := c.eb.PutRule(ctx, &awseb.PutRuleInput{
		Name:         aws.String("r"),
		EventPattern: aws.String(`{"source":["my.app"],"detail":{"state":["running"]}}`),
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if _, err := c.eb.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule: aws.String("r"),
		Targets: []ebtypes.Target{
			{Id: aws.String("lambda"), Arn: aws.String(fnARN)},
			{Id: aws.String("sns"), Arn: aws.String(topicARN)},
		},
	}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	// A non-matching event must fire neither target.
	if _, err := c.eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("my.app"), DetailType: aws.String("t"), Detail: aws.String(`{"state":"stopped"}`)},
	}}); err != nil {
		t.Fatalf("PutEvents(stopped): %v", err)
	}

	if _, count := receiveOne(t, c.sqs, queueURL); count != 0 {
		t.Fatalf("non-matching event delivered %d SNS messages, want 0", count)
	}

	select {
	case p := <-invocations:
		t.Fatalf("non-matching event invoked Lambda: %s", p)
	default:
	}

	// A matching event must reach both the Lambda target and the SNS target.
	if _, err := c.eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("my.app"), DetailType: aws.String("t"), Detail: aws.String(`{"state":"running"}`)},
	}}); err != nil {
		t.Fatalf("PutEvents(running): %v", err)
	}

	select {
	case <-invocations:
	case <-time.After(2 * time.Second):
		t.Fatal("matching event did not invoke the Lambda target")
	}

	if _, count := receiveOne(t, c.sqs, queueURL); count != 1 {
		t.Fatalf("SNS target fan-out delivered %d messages, want 1", count)
	}
}
