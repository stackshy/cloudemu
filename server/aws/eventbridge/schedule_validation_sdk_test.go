package eventbridge_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/aws/smithy-go"
)

// TestSDKEventBridgePutRuleInvalidSchedule verifies PutRule rejects a malformed
// ScheduleExpression with ValidationException, matching real EventBridge, rather
// than storing an expression that would never self-trigger.
func TestSDKEventBridgePutRuleInvalidSchedule(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	cases := []struct {
		name string
		expr string
	}{
		{"natural-language", "every 5 minutes"},
		{"missing-wrapper", "rate 5 minutes"},
		{"rate-plural-mismatch", "rate(1 minutes)"},
		{"rate-singular-mismatch", "rate(5 minute)"},
		{"rate-zero", "rate(0 minutes)"},
		{"rate-non-numeric", "rate(five minutes)"},
		{"rate-bad-unit", "rate(5 fortnights)"},
		{"cron-five-fields", "cron(0 12 * * ?)"},
		{"empty-rate", "rate()"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.PutRule(ctx, &awseb.PutRuleInput{
				Name:               aws.String("sched-" + tc.name),
				ScheduleExpression: aws.String(tc.expr),
			})
			if err == nil {
				t.Fatalf("PutRule(%q) should fail, got nil", tc.expr)
			}

			var apiErr smithy.APIError
			if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ValidationException" {
				t.Fatalf("want ValidationException, got %T: %v", err, err)
			}
		})
	}
}

// TestSDKEventBridgePutRuleValidScheduleRoundTrip verifies valid rate() and
// cron() expressions are accepted and echoed back verbatim by DescribeRule,
// which Terraform reads on every refresh.
func TestSDKEventBridgePutRuleValidScheduleRoundTrip(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	valid := []string{
		"rate(1 minute)",
		"rate(5 minutes)",
		"rate(1 hour)",
		"rate(2 hours)",
		"rate(1 day)",
		"cron(0 12 * * ? *)",
		"cron(15 10 ? * 6L 2019-2022)",
	}

	for i, expr := range valid {
		name := "good-sched-" + string(rune('a'+i))

		if _, err := client.PutRule(ctx, &awseb.PutRuleInput{
			Name:               aws.String(name),
			ScheduleExpression: aws.String(expr),
		}); err != nil {
			t.Fatalf("PutRule(%q) should succeed, got %v", expr, err)
		}

		desc, err := client.DescribeRule(ctx, &awseb.DescribeRuleInput{Name: aws.String(name)})
		if err != nil {
			t.Fatalf("DescribeRule(%s): %v", name, err)
		}

		if aws.ToString(desc.ScheduleExpression) != expr {
			t.Fatalf("ScheduleExpression = %q, want %q", aws.ToString(desc.ScheduleExpression), expr)
		}
	}
}

// TestSDKEventBridgePutRuleInvalidState verifies PutRule rejects a State value
// outside the documented enum with ValidationException rather than silently
// storing it.
func TestSDKEventBridgePutRuleInvalidState(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	_, err := client.PutRule(ctx, &awseb.PutRuleInput{
		Name:         aws.String("bad-state"),
		EventPattern: aws.String(`{"source":["aws.ec2"]}`),
		State:        ebtypes.RuleState("enabled"), // lowercase is not a valid enum value
	})
	if err == nil {
		t.Fatal("PutRule with invalid State should fail, got nil")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ValidationException" {
		t.Fatalf("want ValidationException, got %T: %v", err, err)
	}
}

// TestSDKEventBridgePutRuleCloudTrailStateRoundTrip verifies the
// ENABLED_WITH_ALL_CLOUDTRAIL_MANAGEMENT_EVENTS state is accepted and preserved
// through DescribeRule.
func TestSDKEventBridgePutRuleCloudTrailStateRoundTrip(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	if _, err := client.PutRule(ctx, &awseb.PutRuleInput{
		Name:         aws.String("ct-rule"),
		EventPattern: aws.String(`{"source":["aws.ec2"]}`),
		State:        ebtypes.RuleStateEnabledWithAllCloudtrailManagementEvents,
	}); err != nil {
		t.Fatalf("PutRule with CloudTrail-management state: %v", err)
	}

	desc, err := client.DescribeRule(ctx, &awseb.DescribeRuleInput{Name: aws.String("ct-rule")})
	if err != nil {
		t.Fatalf("DescribeRule: %v", err)
	}

	if desc.State != ebtypes.RuleStateEnabledWithAllCloudtrailManagementEvents {
		t.Fatalf("State = %q, want ENABLED_WITH_ALL_CLOUDTRAIL_MANAGEMENT_EVENTS", desc.State)
	}
}
