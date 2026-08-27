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
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
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
		EventPattern: aws.String(`{"source":["myapp"]}`),
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

func TestSDKEventBridgeScheduledRuleRoundTrip(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	if _, err := client.PutRule(ctx, &awseb.PutRuleInput{
		Name:               aws.String("nightly"),
		ScheduleExpression: aws.String("rate(1 day)"),
		RoleArn:            aws.String("arn:aws:iam::000000000000:role/scheduler"),
		State:              ebtypes.RuleStateEnabled,
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	desc, err := client.DescribeRule(ctx, &awseb.DescribeRuleInput{Name: aws.String("nightly")})
	if err != nil {
		t.Fatalf("DescribeRule: %v", err)
	}

	if aws.ToString(desc.ScheduleExpression) != "rate(1 day)" {
		t.Fatalf("ScheduleExpression = %q, want rate(1 day)", aws.ToString(desc.ScheduleExpression))
	}

	if aws.ToString(desc.RoleArn) != "arn:aws:iam::000000000000:role/scheduler" {
		t.Fatalf("RoleArn = %q, want the scheduler role", aws.ToString(desc.RoleArn))
	}
}

func TestSDKEventBridgePutRuleRequiresPatternOrSchedule(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	_, err := client.PutRule(ctx, &awseb.PutRuleInput{Name: aws.String("empty")})
	if err == nil {
		t.Fatal("PutRule with neither EventPattern nor ScheduleExpression should fail")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ValidationException" {
		t.Fatalf("want ValidationException, got %T: %v", err, err)
	}
}

func TestSDKEventBridgeTargetFieldsRoundTrip(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	if _, err := client.PutRule(ctx, &awseb.PutRuleInput{
		Name:         aws.String("with-targets"),
		EventPattern: aws.String(`{"source":["app"]}`),
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if _, err := client.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule: aws.String("with-targets"),
		Targets: []ebtypes.Target{{
			Id:        aws.String("t1"),
			Arn:       aws.String("arn:aws:sqs:us-east-1:000000000000:q"),
			RoleArn:   aws.String("arn:aws:iam::000000000000:role/invoke"),
			InputPath: aws.String("$.detail"),
			DeadLetterConfig: &ebtypes.DeadLetterConfig{
				Arn: aws.String("arn:aws:sqs:us-east-1:000000000000:dlq"),
			},
			RetryPolicy: &ebtypes.RetryPolicy{
				MaximumRetryAttempts:     aws.Int32(3),
				MaximumEventAgeInSeconds: aws.Int32(3600),
			},
		}},
	}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	out, err := client.ListTargetsByRule(ctx, &awseb.ListTargetsByRuleInput{Rule: aws.String("with-targets")})
	if err != nil {
		t.Fatalf("ListTargetsByRule: %v", err)
	}

	if len(out.Targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(out.Targets))
	}

	tg := out.Targets[0]
	if aws.ToString(tg.RoleArn) != "arn:aws:iam::000000000000:role/invoke" {
		t.Fatalf("target RoleArn = %q", aws.ToString(tg.RoleArn))
	}

	if aws.ToString(tg.InputPath) != "$.detail" {
		t.Fatalf("target InputPath = %q", aws.ToString(tg.InputPath))
	}

	if tg.DeadLetterConfig == nil || aws.ToString(tg.DeadLetterConfig.Arn) == "" {
		t.Fatalf("target DeadLetterConfig dropped: %+v", tg.DeadLetterConfig)
	}

	if tg.RetryPolicy == nil || aws.ToInt32(tg.RetryPolicy.MaximumRetryAttempts) != 3 {
		t.Fatalf("target RetryPolicy dropped: %+v", tg.RetryPolicy)
	}
}

func TestSDKEventBridgeListRulesPrefixAndLimit(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	for _, n := range []string{"orders-a", "orders-b", "orders-c", "payments-a"} {
		if _, err := client.PutRule(ctx, &awseb.PutRuleInput{
			Name:         aws.String(n),
			EventPattern: aws.String(`{"source":["app"]}`),
		}); err != nil {
			t.Fatalf("PutRule(%s): %v", n, err)
		}
	}

	first, err := client.ListRules(ctx, &awseb.ListRulesInput{
		NamePrefix: aws.String("orders-"),
		Limit:      aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}

	if len(first.Rules) != 2 {
		t.Fatalf("page 1 rules = %d, want 2", len(first.Rules))
	}

	for _, rl := range first.Rules {
		if got := aws.ToString(rl.Name); len(got) < 7 || got[:7] != "orders-" {
			t.Fatalf("NamePrefix ignored: got rule %q", got)
		}
	}

	if aws.ToString(first.NextToken) == "" {
		t.Fatal("NextToken empty; paginator cannot advance")
	}

	second, err := client.ListRules(ctx, &awseb.ListRulesInput{
		NamePrefix: aws.String("orders-"),
		NextToken:  first.NextToken,
	})
	if err != nil {
		t.Fatalf("ListRules page 2: %v", err)
	}

	if len(second.Rules) != 1 {
		t.Fatalf("page 2 rules = %d, want 1 (3 orders- rules total)", len(second.Rules))
	}
}

func TestSDKEventBridgeCreateBusTagsVisible(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	created, err := client.CreateEventBus(ctx, &awseb.CreateEventBusInput{
		Name: aws.String("tagged-bus"),
		Tags: []ebtypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	})
	if err != nil {
		t.Fatalf("CreateEventBus: %v", err)
	}

	tags, err := client.ListTagsForResource(ctx, &awseb.ListTagsForResourceInput{
		ResourceARN: created.EventBusArn,
	})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	found := false
	for _, tg := range tags.Tags {
		if aws.ToString(tg.Key) == "env" && aws.ToString(tg.Value) == "prod" {
			found = true
		}
	}

	if !found {
		t.Fatalf("create-time tag not visible via ListTagsForResource: %+v", tags.Tags)
	}
}

func TestSDKEventBridgePutEventsMalformedDetail(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	out, err := client.PutEvents(ctx, &awseb.PutEventsInput{
		Entries: []ebtypes.PutEventsRequestEntry{
			{Source: aws.String("app"), DetailType: aws.String("ok"), Detail: aws.String(`{"k":"v"}`)},
			{Source: aws.String("app"), DetailType: aws.String("bad"), Detail: aws.String("not-json")},
		},
	})
	if err != nil {
		t.Fatalf("PutEvents: %v", err)
	}

	if out.FailedEntryCount != 1 {
		t.Fatalf("FailedEntryCount = %d, want 1", out.FailedEntryCount)
	}

	if len(out.Entries) != 2 {
		t.Fatalf("result entries = %d, want 2", len(out.Entries))
	}

	if aws.ToString(out.Entries[0].EventId) == "" {
		t.Fatalf("entry 0 should have an EventId: %+v", out.Entries[0])
	}

	if aws.ToString(out.Entries[1].ErrorCode) != "MalformedDetail" {
		t.Fatalf("entry 1 ErrorCode = %q, want MalformedDetail", aws.ToString(out.Entries[1].ErrorCode))
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
