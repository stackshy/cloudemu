package eventbridge_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
)

// TestSDKEventBridgeBusIsolation locks the fix for the cross-bus leak: a rule on
// a custom bus must only fire for events published to that same bus. An event on
// the default bus must never trigger a custom-bus rule's target, and vice-versa.
func TestSDKEventBridgeBusIsolation(t *testing.T) {
	eb, sqs := newEBAndSQS(t)
	ctx := context.Background()

	url, arn := makeQueue(t, sqs, "eb-custombus")

	if _, err := eb.CreateEventBus(ctx, &awseb.CreateEventBusInput{Name: aws.String("custombus")}); err != nil {
		t.Fatalf("CreateEventBus: %v", err)
	}

	if _, err := eb.PutRule(ctx, &awseb.PutRuleInput{
		Name:         aws.String("r-custom"),
		EventBusName: aws.String("custombus"),
		EventPattern: aws.String(`{"source":["probe.x"]}`),
	}); err != nil {
		t.Fatalf("PutRule(custombus): %v", err)
	}

	if _, err := eb.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule:         aws.String("r-custom"),
		EventBusName: aws.String("custombus"),
		Targets:      []ebtypes.Target{{Id: aws.String("1"), Arn: aws.String(arn)}},
	}); err != nil {
		t.Fatalf("PutTargets(custombus): %v", err)
	}

	// An event on the DEFAULT bus must NOT trigger the custom-bus rule.
	if _, err := eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("probe.x"), DetailType: aws.String("t"), Detail: aws.String(`{}`)},
	}}); err != nil {
		t.Fatalf("PutEvents(default): %v", err)
	}

	if _, count := receiveOne(t, sqs, url); count != 0 {
		t.Fatalf("event on default bus leaked to custom-bus rule: %d messages, want 0", count)
	}

	// An event on the CUSTOM bus DOES trigger the custom-bus rule.
	if _, err := eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{
			Source:       aws.String("probe.x"),
			DetailType:   aws.String("t"),
			Detail:       aws.String(`{}`),
			EventBusName: aws.String("custombus"),
		},
	}}); err != nil {
		t.Fatalf("PutEvents(custombus): %v", err)
	}

	if _, count := receiveOne(t, sqs, url); count != 1 {
		t.Fatalf("event on custom bus delivered %d messages, want 1", count)
	}
}

// TestSDKEventBridgeDefaultBusRuleNotFiredByCustomBus is the reverse direction:
// a rule on the default bus must not fire for an event published to a custom bus.
func TestSDKEventBridgeDefaultBusRuleNotFiredByCustomBus(t *testing.T) {
	eb, sqs := newEBAndSQS(t)
	ctx := context.Background()

	url, arn := makeQueue(t, sqs, "eb-defaultbus")

	if _, err := eb.CreateEventBus(ctx, &awseb.CreateEventBusInput{Name: aws.String("otherbus")}); err != nil {
		t.Fatalf("CreateEventBus: %v", err)
	}

	if _, err := eb.PutRule(ctx, &awseb.PutRuleInput{
		Name:         aws.String("r-default"),
		EventPattern: aws.String(`{"source":["probe.y"]}`),
	}); err != nil {
		t.Fatalf("PutRule(default): %v", err)
	}

	if _, err := eb.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule:    aws.String("r-default"),
		Targets: []ebtypes.Target{{Id: aws.String("1"), Arn: aws.String(arn)}},
	}); err != nil {
		t.Fatalf("PutTargets(default): %v", err)
	}

	if _, err := eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{
			Source:       aws.String("probe.y"),
			DetailType:   aws.String("t"),
			Detail:       aws.String(`{}`),
			EventBusName: aws.String("otherbus"),
		},
	}}); err != nil {
		t.Fatalf("PutEvents(otherbus): %v", err)
	}

	if _, count := receiveOne(t, sqs, url); count != 0 {
		t.Fatalf("event on custom bus leaked to default-bus rule: %d messages, want 0", count)
	}
}
