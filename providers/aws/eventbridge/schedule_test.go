package eventbridge_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	ebprovider "github.com/stackshy/cloudemu/v2/providers/aws/eventbridge"
	sqsprovider "github.com/stackshy/cloudemu/v2/providers/aws/sqs"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

func TestPutRuleScheduleExpressionValidation(t *testing.T) {
	ctx := context.Background()
	eb := ebprovider.New(config.NewOptions())

	invalid := []string{
		"every 5 minutes",
		"rate 5 minutes",
		"rate(1 minutes)",
		"rate(5 minute)",
		"rate(0 minutes)",
		"rate(-1 minutes)",
		"rate(five minutes)",
		"rate(5 fortnights)",
		"rate()",
		"rate(5)",
		"cron(0 12 * * ?)",
		"cron()",
	}

	for _, expr := range invalid {
		_, err := eb.PutRule(ctx, &ebdriver.RuleConfig{Name: "r", ScheduleExpression: expr})
		if err == nil {
			t.Fatalf("PutRule(%q) should fail", expr)
		}

		if !cerrors.IsInvalidArgument(err) {
			t.Fatalf("PutRule(%q): want InvalidArgument, got %v", expr, err)
		}
	}

	valid := []string{
		"rate(1 minute)",
		"rate(5 minutes)",
		"rate(1 hour)",
		"rate(3 hours)",
		"rate(1 day)",
		"rate(30 days)",
		"cron(0 12 * * ? *)",
		"cron(15 10 ? * 6L 2019-2022)",
	}

	for _, expr := range valid {
		if _, err := eb.PutRule(ctx, &ebdriver.RuleConfig{Name: "r", ScheduleExpression: expr}); err != nil {
			t.Fatalf("PutRule(%q) should succeed, got %v", expr, err)
		}
	}
}

func TestPutRuleStateValidation(t *testing.T) {
	ctx := context.Background()
	eb := ebprovider.New(config.NewOptions())

	for _, state := range []string{"enabled", "Enabled", "FOO", "DISABLE"} {
		_, err := eb.PutRule(ctx, &ebdriver.RuleConfig{
			Name: "r", EventPattern: `{"source":["a"]}`, State: state,
		})
		if err == nil || !cerrors.IsInvalidArgument(err) {
			t.Fatalf("PutRule(State=%q): want InvalidArgument, got %v", state, err)
		}
	}

	for _, state := range []string{"", "ENABLED", "DISABLED", "ENABLED_WITH_ALL_CLOUDTRAIL_MANAGEMENT_EVENTS"} {
		if _, err := eb.PutRule(ctx, &ebdriver.RuleConfig{
			Name: "r", EventPattern: `{"source":["a"]}`, State: state,
		}); err != nil {
			t.Fatalf("PutRule(State=%q) should succeed, got %v", state, err)
		}
	}
}

// TestCloudTrailManagementStateDelivers verifies a rule in the
// ENABLED_WITH_ALL_CLOUDTRAIL_MANAGEMENT_EVENTS state matches and delivers
// events, like an ENABLED rule (and unlike a DISABLED one).
func TestCloudTrailManagementStateDelivers(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions()

	sqs := sqsprovider.New(opts)
	eb := ebprovider.New(opts)
	eb.SetSQSDeliverer(sqs)

	q, err := sqs.CreateQueue(ctx, mqdriver.QueueConfig{Name: "target"})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	if _, err := eb.PutRule(ctx, &ebdriver.RuleConfig{
		Name:         "r",
		EventPattern: `{"source":["a"]}`,
		State:        "ENABLED_WITH_ALL_CLOUDTRAIL_MANAGEMENT_EVENTS",
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if err := eb.PutTargets(ctx, "", "r", []ebdriver.Target{{ID: "1", ARN: q.ARN}}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	if _, err := eb.PutEvents(ctx, []ebdriver.Event{{Source: "a", DetailType: "t", Detail: `{}`}}); err != nil {
		t.Fatalf("PutEvents: %v", err)
	}

	msgs, err := sqs.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: q.URL, MaxMessages: 10})
	if err != nil {
		t.Fatalf("ReceiveMessages: %v", err)
	}

	if len(msgs) != 1 {
		t.Fatalf("delivered %d messages, want 1", len(msgs))
	}
}
