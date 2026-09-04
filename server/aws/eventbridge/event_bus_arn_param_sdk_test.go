package eventbridge_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
)

// TestSDKEventBridgeBusARNAcceptedByDescribeEventBus verifies that
// DescribeEventBus's Name parameter accepts the bus's ARN, not just its bare
// name (busNameOrDefault in server/aws/eventbridge/types.go).
func TestSDKEventBridgeBusARNAcceptedByDescribeEventBus(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	created, err := client.CreateEventBus(ctx, &awseb.CreateEventBusInput{Name: aws.String("arn-param-bus")})
	if err != nil {
		t.Fatalf("CreateEventBus: %v", err)
	}

	busARN := aws.ToString(created.EventBusArn)
	if busARN == "" {
		t.Fatal("CreateEventBus returned empty ARN")
	}

	desc, err := client.DescribeEventBus(ctx, &awseb.DescribeEventBusInput{Name: aws.String(busARN)})
	if err != nil {
		t.Fatalf("DescribeEventBus(ARN): %v", err)
	}

	if got := aws.ToString(desc.Name); got != "arn-param-bus" {
		t.Fatalf("DescribeEventBus(ARN) resolved name = %q, want arn-param-bus", got)
	}

	if got := aws.ToString(desc.Arn); got != busARN {
		t.Fatalf("DescribeEventBus(ARN) arn = %q, want %q", got, busARN)
	}
}

// TestSDKEventBridgeBusARNAcceptedByPutRule verifies that PutRule's
// EventBusName parameter accepts the bus's ARN, matching real EventBridge's
// "The name or ARN of the event bus" contract for EventBusName-shaped params.
func TestSDKEventBridgeBusARNAcceptedByPutRule(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	created, err := client.CreateEventBus(ctx, &awseb.CreateEventBusInput{Name: aws.String("rule-arn-param-bus")})
	if err != nil {
		t.Fatalf("CreateEventBus: %v", err)
	}

	busARN := aws.ToString(created.EventBusArn)

	if _, err := client.PutRule(ctx, &awseb.PutRuleInput{
		Name:         aws.String("r"),
		EventBusName: aws.String(busARN),
		EventPattern: aws.String(`{"source":["app"]}`),
	}); err != nil {
		t.Fatalf("PutRule(EventBusName=ARN): %v", err)
	}

	// The rule must be visible when listing rules on the bus by its plain
	// name — i.e. it resolved to the same bus, not a distinct one keyed by
	// the literal ARN string.
	rules, err := client.ListRules(ctx, &awseb.ListRulesInput{EventBusName: aws.String("rule-arn-param-bus")})
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}

	if len(rules.Rules) != 1 || aws.ToString(rules.Rules[0].Name) != "r" {
		t.Fatalf("ListRules(by name) = %+v, want one rule %q", rules.Rules, "r")
	}
}

// TestSDKEventBridgeBusARNAcceptedByPutTargetsAndPutEvents verifies that
// PutTargets and PutEvents accept the bus ARN in their EventBusName
// parameter, and that doing so reaches the same bus as the plain name (a rule
// created on the plain name receives events published via the ARN).
func TestSDKEventBridgeBusARNAcceptedByPutTargetsAndPutEvents(t *testing.T) {
	eb, sqs := newEBAndSQS(t)
	ctx := context.Background()

	created, err := eb.CreateEventBus(ctx, &awseb.CreateEventBusInput{Name: aws.String("targets-events-arn-bus")})
	if err != nil {
		t.Fatalf("CreateEventBus: %v", err)
	}

	busARN := aws.ToString(created.EventBusArn)

	url, arn := makeQueue(t, sqs, "eb-arn-param-target")

	if _, err := eb.PutRule(ctx, &awseb.PutRuleInput{
		Name:         aws.String("r"),
		EventBusName: aws.String("targets-events-arn-bus"),
		EventPattern: aws.String(`{"source":["app"]}`),
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	// PutTargets addresses the bus by ARN.
	if _, err := eb.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule:         aws.String("r"),
		EventBusName: aws.String(busARN),
		Targets:      []ebtypes.Target{{Id: aws.String("1"), Arn: aws.String(arn)}},
	}); err != nil {
		t.Fatalf("PutTargets(EventBusName=ARN): %v", err)
	}

	// PutEvents also addresses the bus by ARN.
	if _, err := eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("app"), DetailType: aws.String("t"), Detail: aws.String(`{}`), EventBusName: aws.String(busARN)},
	}}); err != nil {
		t.Fatalf("PutEvents(EventBusName=ARN): %v", err)
	}

	if _, count := receiveOne(t, sqs, url); count != 1 {
		t.Fatalf("delivered %d messages, want 1 (bus ARN must resolve to the same bus as its name)", count)
	}
}
