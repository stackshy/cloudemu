package eventbridge_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newEBAndSQS wires a single wire server serving both EventBridge and SQS from
// one cloud, so PutEvents can fan out to a real SQS queue over the SDKs.
func newEBAndSQS(t *testing.T) (*awseb.Client, *awssqs.Client) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{EventBridge: cloud.EventBridge, SQS: cloud.SQS})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	eb := awseb.NewFromConfig(cfg, func(o *awseb.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	sqs := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	return eb, sqs
}

func makeQueue(t *testing.T, sqs *awssqs.Client, name string) (url, arn string) {
	t.Helper()

	ctx := context.Background()

	q, err := sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String(name)})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	attrs, err := sqs.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl:       q.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})
	if err != nil {
		t.Fatalf("GetQueueAttributes: %v", err)
	}

	return aws.ToString(q.QueueUrl), attrs.Attributes["QueueArn"]
}

func receiveOne(t *testing.T, sqs *awssqs.Client, url string) (body string, count int) {
	t.Helper()

	out, err := sqs.ReceiveMessage(context.Background(), &awssqs.ReceiveMessageInput{
		QueueUrl:            aws.String(url),
		MaxNumberOfMessages: 10,
	})
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}

	if len(out.Messages) == 0 {
		return "", 0
	}

	return aws.ToString(out.Messages[0].Body), len(out.Messages)
}

func TestSDKEventBridgeNestedDetailDelivery(t *testing.T) {
	eb, sqs := newEBAndSQS(t)
	ctx := context.Background()

	url, arn := makeQueue(t, sqs, "eb-detail")

	if _, err := eb.PutRule(ctx, &awseb.PutRuleInput{
		Name:         aws.String("r"),
		EventPattern: aws.String(`{"source":["my.app"],"detail":{"state":["running"]}}`),
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if _, err := eb.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule:    aws.String("r"),
		Targets: []ebtypes.Target{{Id: aws.String("1"), Arn: aws.String(arn)}},
	}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	// Non-matching detail must not be delivered.
	if _, err := eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("my.app"), DetailType: aws.String("t"), Detail: aws.String(`{"state":"stopped"}`)},
	}}); err != nil {
		t.Fatalf("PutEvents(stopped): %v", err)
	}

	if _, count := receiveOne(t, sqs, url); count != 0 {
		t.Fatalf("non-matching detail delivered %d messages, want 0", count)
	}

	// Matching detail is delivered.
	if _, err := eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("my.app"), DetailType: aws.String("t"), Detail: aws.String(`{"state":"running"}`)},
	}}); err != nil {
		t.Fatalf("PutEvents(running): %v", err)
	}

	if _, count := receiveOne(t, sqs, url); count != 1 {
		t.Fatalf("matching detail delivered %d messages, want 1", count)
	}
}

func TestSDKEventBridgeInputTransformerDelivery(t *testing.T) {
	eb, sqs := newEBAndSQS(t)
	ctx := context.Background()

	url, arn := makeQueue(t, sqs, "eb-transform")

	if _, err := eb.PutRule(ctx, &awseb.PutRuleInput{
		Name:         aws.String("r"),
		EventPattern: aws.String(`{"source":["app"]}`),
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if _, err := eb.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule: aws.String("r"),
		Targets: []ebtypes.Target{{
			Id:  aws.String("1"),
			Arn: aws.String(arn),
			InputTransformer: &ebtypes.InputTransformer{
				InputPathsMap: map[string]string{"st": "$.detail.state"},
				InputTemplate: aws.String(`"state is <st>"`),
			},
		}},
	}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	if _, err := eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("app"), DetailType: aws.String("t"), Detail: aws.String(`{"state":"ok"}`)},
	}}); err != nil {
		t.Fatalf("PutEvents: %v", err)
	}

	body, count := receiveOne(t, sqs, url)
	if count != 1 {
		t.Fatalf("delivered %d messages, want 1", count)
	}

	if body != "state is ok" {
		t.Fatalf("transformed body = %q, want %q", body, "state is ok")
	}
}

func TestSDKEventBridgeConstantInputDelivery(t *testing.T) {
	eb, sqs := newEBAndSQS(t)
	ctx := context.Background()

	url, arn := makeQueue(t, sqs, "eb-const")

	if _, err := eb.PutRule(ctx, &awseb.PutRuleInput{
		Name:         aws.String("r"),
		EventPattern: aws.String(`{"source":["app"]}`),
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if _, err := eb.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule: aws.String("r"),
		Targets: []ebtypes.Target{{
			Id:    aws.String("1"),
			Arn:   aws.String(arn),
			Input: aws.String(`{"hello":"world"}`),
		}},
	}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	if _, err := eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("app"), DetailType: aws.String("t"), Detail: aws.String(`{"state":"ok"}`)},
	}}); err != nil {
		t.Fatalf("PutEvents: %v", err)
	}

	body, count := receiveOne(t, sqs, url)
	if count != 1 {
		t.Fatalf("delivered %d messages, want 1", count)
	}

	if body != `{"hello":"world"}` {
		t.Fatalf("constant input body = %q, want the constant", body)
	}
}

// TestSDKEventBridgeReservedTransformerVars verifies that reserved transformer
// variables (which need not be declared in InputPathsMap) are substituted:
// <aws.events.event.json> embeds the full event, and the mixed user/reserved
// template still yields valid JSON.
func TestSDKEventBridgeReservedTransformerVars(t *testing.T) {
	eb, sqs := newEBAndSQS(t)
	ctx := context.Background()

	url, arn := makeQueue(t, sqs, "eb-reserved")

	if _, err := eb.PutRule(ctx, &awseb.PutRuleInput{
		Name:         aws.String("r"),
		EventPattern: aws.String(`{"source":["app"]}`),
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if _, err := eb.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule: aws.String("r"),
		Targets: []ebtypes.Target{{
			Id:  aws.String("1"),
			Arn: aws.String(arn),
			InputTransformer: &ebtypes.InputTransformer{
				InputPathsMap: map[string]string{"st": "$.detail.state"},
				InputTemplate: aws.String(
					`{"whole":<aws.events.event.json>,"picked":"<st>","rule":<aws.events.rule-name>}`),
			},
		}},
	}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	if _, err := eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("app"), DetailType: aws.String("t"), Detail: aws.String(`{"state":"ok"}`)},
	}}); err != nil {
		t.Fatalf("PutEvents: %v", err)
	}

	body, count := receiveOne(t, sqs, url)
	if count != 1 {
		t.Fatalf("delivered %d messages, want 1", count)
	}

	// The reserved variable must be substituted, not left as literal text, so the
	// delivered body parses as JSON.
	var got struct {
		Whole  map[string]any `json:"whole"`
		Picked string         `json:"picked"`
		Rule   string         `json:"rule"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("delivered body is not valid JSON (%v): %s", err, body)
	}

	if got.Picked != "ok" {
		t.Fatalf("picked = %q, want %q", got.Picked, "ok")
	}

	if got.Rule != "r" {
		t.Fatalf("rule = %q, want %q (aws.events.rule-name)", got.Rule, "r")
	}

	if got.Whole["source"] != "app" {
		t.Fatalf("aws.events.event.json did not embed the full event: %s", body)
	}
}

// TestSDKEventBridgeResourcesAlwaysArray verifies the delivered envelope carries
// "resources" as an empty array (never null) when the event has none.
func TestSDKEventBridgeResourcesAlwaysArray(t *testing.T) {
	eb, sqs := newEBAndSQS(t)
	ctx := context.Background()

	url, arn := makeQueue(t, sqs, "eb-resources")

	if _, err := eb.PutRule(ctx, &awseb.PutRuleInput{
		Name:         aws.String("r"),
		EventPattern: aws.String(`{"source":["app"]}`),
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if _, err := eb.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule:    aws.String("r"),
		Targets: []ebtypes.Target{{Id: aws.String("1"), Arn: aws.String(arn)}},
	}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	if _, err := eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("app"), DetailType: aws.String("t"), Detail: aws.String(`{"state":"ok"}`)},
	}}); err != nil {
		t.Fatalf("PutEvents: %v", err)
	}

	body, count := receiveOne(t, sqs, url)
	if count != 1 {
		t.Fatalf("delivered %d messages, want 1", count)
	}

	var env map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("envelope not JSON: %v", err)
	}

	raw, ok := env["resources"]
	if !ok {
		t.Fatalf("envelope missing resources field: %s", body)
	}

	if string(raw) != "[]" {
		t.Fatalf("resources = %s, want [] (never null)", raw)
	}

	// A strict decode into a slice must succeed and range cleanly.
	var res []string
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("resources did not decode as an array: %v", err)
	}

	if len(res) != 0 {
		t.Fatalf("resources len = %d, want 0", len(res))
	}
}
