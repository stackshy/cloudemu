package eventbridge_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// ruleARNTestAccountID and ruleARNTestRegion are threaded explicitly into
// Drivers so the tests below can assert against an exact ARN string — the
// package's other helpers (e.g. newEventBridgeClient) leave AccountID/Region
// blank, which is fine for existence checks but not for exact-format ones.
const (
	ruleARNTestAccountID = "123456789012"
	ruleARNTestRegion    = "us-east-1"
)

// newEBClientWithIdentity returns an EventBridge client backed by a server
// with a fixed account ID and region, for exact-ARN assertions.
func newEBClientWithIdentity(t *testing.T) *awseb.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		EventBridge: cloud.EventBridge,
		AccountID:   ruleARNTestAccountID,
		Region:      ruleARNTestRegion,
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(ruleARNTestRegion),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awseb.NewFromConfig(cfg, func(o *awseb.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

// newEBAndSQSWithIdentity is like newEBAndSQS (delivery_sdk_test.go) but with
// a fixed account ID and region, for tests that need to inspect an exact ARN
// carried through delivery (e.g. the <aws.events.rule-arn> reserved
// variable).
func newEBAndSQSWithIdentity(t *testing.T) (*awseb.Client, *awssqs.Client) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		EventBridge: cloud.EventBridge,
		SQS:         cloud.SQS,
		AccountID:   ruleARNTestAccountID,
		Region:      ruleARNTestRegion,
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(ruleARNTestRegion),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	eb := awseb.NewFromConfig(cfg, func(o *awseb.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	sqs := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	return eb, sqs
}

// TestSDKEventBridgeDefaultBusRuleARNOmitsBusSegment verifies that a rule on
// the (implicit) default bus gets an ARN with no bus segment:
// "arn:aws:events:<region>:<account>:rule/<rule>", not
// "arn:aws:events:<region>:<account>:rule/default/<rule>".
func TestSDKEventBridgeDefaultBusRuleARNOmitsBusSegment(t *testing.T) {
	client := newEBClientWithIdentity(t)
	ctx := context.Background()

	rule, err := client.PutRule(ctx, &awseb.PutRuleInput{
		Name:         aws.String("implicit-default"),
		EventPattern: aws.String(`{"source":["app"]}`),
	})
	if err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	want := "arn:aws:events:us-east-1:123456789012:rule/implicit-default"
	if got := aws.ToString(rule.RuleArn); got != want {
		t.Fatalf("RuleArn = %q, want %q (no /default/ segment)", got, want)
	}
}

// TestSDKEventBridgeExplicitDefaultBusRuleARNOmitsBusSegment is the same as
// above but with EventBusName explicitly set to "default" — the fix's
// bus == defaultBusName branch, distinct from the bus == "" branch.
func TestSDKEventBridgeExplicitDefaultBusRuleARNOmitsBusSegment(t *testing.T) {
	client := newEBClientWithIdentity(t)
	ctx := context.Background()

	rule, err := client.PutRule(ctx, &awseb.PutRuleInput{
		Name:         aws.String("explicit-default"),
		EventBusName: aws.String("default"),
		EventPattern: aws.String(`{"source":["app"]}`),
	})
	if err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	want := "arn:aws:events:us-east-1:123456789012:rule/explicit-default"
	if got := aws.ToString(rule.RuleArn); got != want {
		t.Fatalf("RuleArn = %q, want %q (no /default/ segment)", got, want)
	}
}

// TestSDKEventBridgeCustomBusRuleARNIncludesBusSegment verifies a rule on a
// custom (non-default) bus still gets the bus segment in its ARN.
func TestSDKEventBridgeCustomBusRuleARNIncludesBusSegment(t *testing.T) {
	client := newEBClientWithIdentity(t)
	ctx := context.Background()

	if _, err := client.CreateEventBus(ctx, &awseb.CreateEventBusInput{Name: aws.String("orders-bus")}); err != nil {
		t.Fatalf("CreateEventBus: %v", err)
	}

	rule, err := client.PutRule(ctx, &awseb.PutRuleInput{
		Name:         aws.String("on-custom-bus"),
		EventBusName: aws.String("orders-bus"),
		EventPattern: aws.String(`{"source":["app"]}`),
	})
	if err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	want := "arn:aws:events:us-east-1:123456789012:rule/orders-bus/on-custom-bus"
	if got := aws.ToString(rule.RuleArn); got != want {
		t.Fatalf("RuleArn = %q, want %q", got, want)
	}
}

// TestSDKEventBridgeReservedRuleARNVarOmitsBusSegment guards the driver-side
// ruleARN (providers/aws/eventbridge/eventbridge.go), which is exercised via
// the <aws.events.rule-arn> reserved transformer variable rather than any API
// response field. A default-bus rule's delivered rule-arn must carry no
// "/default/" segment.
func TestSDKEventBridgeReservedRuleARNVarOmitsBusSegment(t *testing.T) {
	eb, sqs := newEBAndSQSWithIdentity(t)
	ctx := context.Background()

	url, arn := makeQueue(t, sqs, "eb-rule-arn-var")

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
				InputTemplate: aws.String(`"<aws.events.rule-arn>"`),
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

	want := "arn:aws:events:us-east-1:123456789012:rule/r"
	if body != want {
		t.Fatalf("delivered aws.events.rule-arn = %s, want %s (no /default/ segment)", body, want)
	}
}
