package eventbridge_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"

	"github.com/stackshy/cloudemu"
	awsserver "github.com/stackshy/cloudemu/server/aws"
)

func newEventBridgeClient(t *testing.T) *awseb.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{EventBridge: cloud.EventBridge})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awseb.NewFromConfig(cfg, func(o *awseb.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKEventBridgeLifecycle(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	created, err := client.CreateEventBus(ctx, &awseb.CreateEventBusInput{
		Name: aws.String("orders-bus"),
		Tags: []ebtypes.Tag{{Key: aws.String("env"), Value: aws.String("test")}},
	})
	if err != nil {
		t.Fatalf("CreateEventBus: %v", err)
	}

	if aws.ToString(created.EventBusArn) == "" {
		t.Fatalf("CreateEventBus returned empty ARN: %+v", created)
	}

	desc, err := client.DescribeEventBus(ctx, &awseb.DescribeEventBusInput{Name: aws.String("orders-bus")})
	if err != nil {
		t.Fatalf("DescribeEventBus: %v", err)
	}

	if aws.ToString(desc.Name) != "orders-bus" {
		t.Fatalf("DescribeEventBus name = %q, want orders-bus", aws.ToString(desc.Name))
	}

	rule, err := client.PutRule(ctx, &awseb.PutRuleInput{
		Name:         aws.String("order-created"),
		EventBusName: aws.String("orders-bus"),
		EventPattern: aws.String(`{"source":["orders"]}`),
		State:        ebtypes.RuleStateEnabled,
	})
	if err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if aws.ToString(rule.RuleArn) == "" {
		t.Fatalf("PutRule returned empty RuleArn")
	}

	if _, err := client.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule:         aws.String("order-created"),
		EventBusName: aws.String("orders-bus"),
		Targets: []ebtypes.Target{
			{Id: aws.String("t1"), Arn: aws.String("arn:aws:lambda:us-east-1:111122223333:function:handler")},
		},
	}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	targets, err := client.ListTargetsByRule(ctx, &awseb.ListTargetsByRuleInput{
		Rule:         aws.String("order-created"),
		EventBusName: aws.String("orders-bus"),
	})
	if err != nil {
		t.Fatalf("ListTargetsByRule: %v", err)
	}

	if len(targets.Targets) != 1 || aws.ToString(targets.Targets[0].Id) != "t1" {
		t.Fatalf("ListTargetsByRule = %+v, want one target t1", targets.Targets)
	}

	put, err := client.PutEvents(ctx, &awseb.PutEventsInput{
		Entries: []ebtypes.PutEventsRequestEntry{
			{
				Source:       aws.String("orders"),
				DetailType:   aws.String("OrderCreated"),
				Detail:       aws.String(`{"orderId":"1"}`),
				EventBusName: aws.String("orders-bus"),
			},
		},
	})
	if err != nil {
		t.Fatalf("PutEvents: %v", err)
	}

	if put.FailedEntryCount != 0 {
		t.Fatalf("PutEvents FailedEntryCount = %d, want 0", put.FailedEntryCount)
	}

	if len(put.Entries) != 1 || aws.ToString(put.Entries[0].EventId) == "" {
		t.Fatalf("PutEvents Entries = %+v, want one entry with EventId", put.Entries)
	}

	rules, err := client.ListRules(ctx, &awseb.ListRulesInput{EventBusName: aws.String("orders-bus")})
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}

	if len(rules.Rules) != 1 || aws.ToString(rules.Rules[0].Name) != "order-created" {
		t.Fatalf("ListRules = %+v, want one rule order-created", rules.Rules)
	}

	buses, err := client.ListEventBuses(ctx, &awseb.ListEventBusesInput{})
	if err != nil {
		t.Fatalf("ListEventBuses: %v", err)
	}

	// The driver seeds a "default" bus, so we expect it plus orders-bus.
	if !containsBus(buses.EventBuses, "orders-bus") {
		t.Fatalf("ListEventBuses = %+v, want orders-bus present", buses.EventBuses)
	}

	if _, err := client.RemoveTargets(ctx, &awseb.RemoveTargetsInput{
		Rule:         aws.String("order-created"),
		EventBusName: aws.String("orders-bus"),
		Ids:          []string{"t1"},
	}); err != nil {
		t.Fatalf("RemoveTargets: %v", err)
	}

	if _, err := client.DeleteRule(ctx, &awseb.DeleteRuleInput{
		Name:         aws.String("order-created"),
		EventBusName: aws.String("orders-bus"),
	}); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}

	if _, err := client.DeleteEventBus(ctx, &awseb.DeleteEventBusInput{Name: aws.String("orders-bus")}); err != nil {
		t.Fatalf("DeleteEventBus: %v", err)
	}

	if _, err := client.DescribeEventBus(ctx, &awseb.DescribeEventBusInput{Name: aws.String("orders-bus")}); err == nil {
		t.Fatal("DescribeEventBus after delete: want error, got nil")
	}
}

func TestSDKEventBridgeRuleState(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	if _, err := client.CreateEventBus(ctx, &awseb.CreateEventBusInput{Name: aws.String("b")}); err != nil {
		t.Fatalf("CreateEventBus: %v", err)
	}

	if _, err := client.PutRule(ctx, &awseb.PutRuleInput{
		Name:         aws.String("r"),
		EventBusName: aws.String("b"),
		State:        ebtypes.RuleStateEnabled,
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if _, err := client.DisableRule(ctx, &awseb.DisableRuleInput{
		Name: aws.String("r"), EventBusName: aws.String("b"),
	}); err != nil {
		t.Fatalf("DisableRule: %v", err)
	}

	got, err := client.DescribeRule(ctx, &awseb.DescribeRuleInput{
		Name: aws.String("r"), EventBusName: aws.String("b"),
	})
	if err != nil {
		t.Fatalf("DescribeRule: %v", err)
	}

	if got.State != ebtypes.RuleStateDisabled {
		t.Fatalf("rule state = %q, want DISABLED", got.State)
	}

	if _, err := client.EnableRule(ctx, &awseb.EnableRuleInput{
		Name: aws.String("r"), EventBusName: aws.String("b"),
	}); err != nil {
		t.Fatalf("EnableRule: %v", err)
	}

	got, err = client.DescribeRule(ctx, &awseb.DescribeRuleInput{
		Name: aws.String("r"), EventBusName: aws.String("b"),
	})
	if err != nil {
		t.Fatalf("DescribeRule: %v", err)
	}

	if got.State != ebtypes.RuleStateEnabled {
		t.Fatalf("rule state = %q, want ENABLED", got.State)
	}
}

func TestSDKEventBridgeErrors(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	if _, err := client.CreateEventBus(ctx, &awseb.CreateEventBusInput{Name: aws.String("dup")}); err != nil {
		t.Fatalf("CreateEventBus: %v", err)
	}

	_, err := client.CreateEventBus(ctx, &awseb.CreateEventBusInput{Name: aws.String("dup")})

	var exists *ebtypes.ResourceAlreadyExistsException
	if !errors.As(err, &exists) {
		t.Fatalf("duplicate CreateEventBus: got %v, want ResourceAlreadyExistsException", err)
	}

	_, err = client.DescribeEventBus(ctx, &awseb.DescribeEventBusInput{Name: aws.String("missing")})

	var notFound *ebtypes.ResourceNotFoundException
	if !errors.As(err, &notFound) {
		t.Fatalf("DescribeEventBus(missing): got %v, want ResourceNotFoundException", err)
	}
}

func containsBus(buses []ebtypes.EventBus, name string) bool {
	for i := range buses {
		if aws.ToString(buses[i].Name) == name {
			return true
		}
	}

	return false
}
