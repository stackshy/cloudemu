package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestAWSEventBusCompat drives an EventBridge bus, rule, target, and event
// lifecycle through the real aws-sdk-go-v2 client against CloudEmu's in-process
// wire server. Operation names match the portable "eventbus" driver
// (DescribeEventBus → "GetEventBus", DescribeRule → "GetRule",
// ListTargetsByRule → "ListTargets").
func TestAWSEventBusCompat(t *testing.T) {
	provider := cloudemu.NewAWS()
	sess := compat.BootAWS(t, awsserver.Drivers{EventBridge: provider.EventBridge})
	client := awseb.NewFromConfig(sess.Config(), func(o *awseb.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})
	ctx := context.Background()

	const svc = "eventbus"

	const (
		busName  = "compat-bus"
		ruleName = "compat-rule"
	)

	sess.Op(svc, "CreateEventBus", func() error {
		_, err := client.CreateEventBus(ctx, &awseb.CreateEventBusInput{
			Name: aws.String(busName),
			Tags: []ebtypes.Tag{{Key: aws.String("env"), Value: aws.String("test")}},
		})

		return err
	})

	sess.Op(svc, "GetEventBus", func() error {
		_, err := client.DescribeEventBus(ctx, &awseb.DescribeEventBusInput{Name: aws.String(busName)})
		return err
	})

	sess.Op(svc, "ListEventBuses", func() error {
		_, err := client.ListEventBuses(ctx, &awseb.ListEventBusesInput{})
		return err
	})

	sess.Op(svc, "PutRule", func() error {
		_, err := client.PutRule(ctx, &awseb.PutRuleInput{
			Name:         aws.String(ruleName),
			EventBusName: aws.String(busName),
			EventPattern: aws.String(`{"source":["orders"]}`),
			State:        ebtypes.RuleStateEnabled,
		})

		return err
	})

	sess.Op(svc, "GetRule", func() error {
		_, err := client.DescribeRule(ctx, &awseb.DescribeRuleInput{
			Name:         aws.String(ruleName),
			EventBusName: aws.String(busName),
		})

		return err
	})

	sess.Op(svc, "ListRules", func() error {
		_, err := client.ListRules(ctx, &awseb.ListRulesInput{EventBusName: aws.String(busName)})
		return err
	})

	sess.Op(svc, "PutTargets", func() error {
		_, err := client.PutTargets(ctx, &awseb.PutTargetsInput{
			Rule:         aws.String(ruleName),
			EventBusName: aws.String(busName),
			Targets: []ebtypes.Target{
				{Id: aws.String("t1"), Arn: aws.String("arn:aws:lambda:us-east-1:111122223333:function:handler")},
			},
		})

		return err
	})

	sess.Op(svc, "ListTargets", func() error {
		_, err := client.ListTargetsByRule(ctx, &awseb.ListTargetsByRuleInput{
			Rule:         aws.String(ruleName),
			EventBusName: aws.String(busName),
		})

		return err
	})

	sess.Op(svc, "DisableRule", func() error {
		_, err := client.DisableRule(ctx, &awseb.DisableRuleInput{
			Name:         aws.String(ruleName),
			EventBusName: aws.String(busName),
		})

		return err
	})

	sess.Op(svc, "EnableRule", func() error {
		_, err := client.EnableRule(ctx, &awseb.EnableRuleInput{
			Name:         aws.String(ruleName),
			EventBusName: aws.String(busName),
		})

		return err
	})

	sess.Op(svc, "PutEvents", func() error {
		_, err := client.PutEvents(ctx, &awseb.PutEventsInput{
			Entries: []ebtypes.PutEventsRequestEntry{
				{
					Source:       aws.String("orders"),
					DetailType:   aws.String("OrderCreated"),
					Detail:       aws.String(`{"orderId":"1"}`),
					EventBusName: aws.String(busName),
				},
			},
		})

		return err
	})

	sess.Op(svc, "RemoveTargets", func() error {
		_, err := client.RemoveTargets(ctx, &awseb.RemoveTargetsInput{
			Rule:         aws.String(ruleName),
			EventBusName: aws.String(busName),
			Ids:          []string{"t1"},
		})

		return err
	})

	sess.Op(svc, "DeleteRule", func() error {
		_, err := client.DeleteRule(ctx, &awseb.DeleteRuleInput{
			Name:         aws.String(ruleName),
			EventBusName: aws.String(busName),
		})

		return err
	})

	sess.Op(svc, "DeleteEventBus", func() error {
		_, err := client.DeleteEventBus(ctx, &awseb.DeleteEventBusInput{Name: aws.String(busName)})
		return err
	})
}
