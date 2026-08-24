package elbv2_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/smithy-go"
)

// apiErrorCode returns the smithy API error code for err, failing the test if
// err is not an API error.
func apiErrorCode(t *testing.T, err error) string {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want API error, got %v", err)
	}

	return apiErr.ErrorCode()
}

// createLBAndTG is a helper that creates a load balancer and a forward target
// group, returning their ARNs.
func createLBAndTG(ctx context.Context, t *testing.T, client *elb.Client, lbName, tgName string) (string, string) {
	t.Helper()

	lbOut, err := client.CreateLoadBalancer(ctx, &elb.CreateLoadBalancerInput{
		Name:    aws.String(lbName),
		Subnets: []string{"subnet-a"},
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer: %v", err)
	}

	tgOut, err := client.CreateTargetGroup(ctx, &elb.CreateTargetGroupInput{
		Name:     aws.String(tgName),
		Protocol: elbtypes.ProtocolEnumHttp,
		Port:     aws.Int32(80),
		VpcId:    aws.String("vpc-1"),
	})
	if err != nil {
		t.Fatalf("CreateTargetGroup: %v", err)
	}

	return aws.ToString(lbOut.LoadBalancers[0].LoadBalancerArn),
		aws.ToString(tgOut.TargetGroups[0].TargetGroupArn)
}

// TestSDKDeleteTargetGroupInUse proves a target group referenced by a listener
// default action (and a rule action) cannot be deleted: real ELBv2 rejects it
// with ResourceInUse.
func TestSDKDeleteTargetGroupInUse(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	lbARN, tgARN := createLBAndTG(ctx, t, client, "inuse-alb", "inuse-tg")

	liOut, err := client.CreateListener(ctx, &elb.CreateListenerInput{
		LoadBalancerArn: aws.String(lbARN),
		Protocol:        elbtypes.ProtocolEnumHttp,
		Port:            aws.Int32(80),
		DefaultActions: []elbtypes.Action{{
			Type:           elbtypes.ActionTypeEnumForward,
			TargetGroupArn: aws.String(tgARN),
		}},
	})
	if err != nil {
		t.Fatalf("CreateListener: %v", err)
	}

	if _, err := client.CreateRule(ctx, &elb.CreateRuleInput{
		ListenerArn: liOut.Listeners[0].ListenerArn,
		Priority:    aws.Int32(10),
		Conditions: []elbtypes.RuleCondition{{
			Field:  aws.String("path-pattern"),
			Values: []string{"/api/*"},
		}},
		Actions: []elbtypes.Action{{
			Type:           elbtypes.ActionTypeEnumForward,
			TargetGroupArn: aws.String(tgARN),
		}},
	}); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	_, err = client.DeleteTargetGroup(ctx, &elb.DeleteTargetGroupInput{
		TargetGroupArn: aws.String(tgARN),
	})
	if code := apiErrorCode(t, err); code != "ResourceInUse" {
		t.Fatalf("DeleteTargetGroup(in use) code = %q, want ResourceInUse", code)
	}
}

// TestSDKCreateListenerBogusTargetGroup proves a default action forwarding to a
// non-existent target group fails with TargetGroupNotFound.
func TestSDKCreateListenerBogusTargetGroup(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	lbOut, err := client.CreateLoadBalancer(ctx, &elb.CreateLoadBalancerInput{
		Name:    aws.String("bogus-listener-alb"),
		Subnets: []string{"subnet-a"},
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer: %v", err)
	}

	_, err = client.CreateListener(ctx, &elb.CreateListenerInput{
		LoadBalancerArn: lbOut.LoadBalancers[0].LoadBalancerArn,
		Protocol:        elbtypes.ProtocolEnumHttp,
		Port:            aws.Int32(80),
		DefaultActions: []elbtypes.Action{{
			Type: elbtypes.ActionTypeEnumForward,
			TargetGroupArn: aws.String(
				"arn:aws:elasticloadbalancing:us-east-1:000000000000:targetgroup/nope/0000000000000000"),
		}},
	})
	if code := apiErrorCode(t, err); code != "TargetGroupNotFound" {
		t.Fatalf("CreateListener(bogus TG) code = %q, want TargetGroupNotFound", code)
	}
}

// TestSDKCreateRuleBogusTargetGroup proves a forward action referencing a
// non-existent target group fails with TargetGroupNotFound.
func TestSDKCreateRuleBogusTargetGroup(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	lbARN, tgARN := createLBAndTG(ctx, t, client, "bogus-rule-alb", "bogus-rule-tg")

	liOut, err := client.CreateListener(ctx, &elb.CreateListenerInput{
		LoadBalancerArn: aws.String(lbARN),
		Protocol:        elbtypes.ProtocolEnumHttp,
		Port:            aws.Int32(80),
		DefaultActions: []elbtypes.Action{{
			Type:           elbtypes.ActionTypeEnumForward,
			TargetGroupArn: aws.String(tgARN),
		}},
	})
	if err != nil {
		t.Fatalf("CreateListener: %v", err)
	}

	_, err = client.CreateRule(ctx, &elb.CreateRuleInput{
		ListenerArn: liOut.Listeners[0].ListenerArn,
		Priority:    aws.Int32(20),
		Conditions: []elbtypes.RuleCondition{{
			Field:  aws.String("path-pattern"),
			Values: []string{"/x/*"},
		}},
		Actions: []elbtypes.Action{{
			Type: elbtypes.ActionTypeEnumForward,
			TargetGroupArn: aws.String(
				"arn:aws:elasticloadbalancing:us-east-1:000000000000:targetgroup/nope/0000000000000000"),
		}},
	})
	if code := apiErrorCode(t, err); code != "TargetGroupNotFound" {
		t.Fatalf("CreateRule(bogus TG) code = %q, want TargetGroupNotFound", code)
	}
}

// TestSDKCreateRuleDuplicatePriority proves a listener can't have two rules with
// the same priority: the second CreateRule fails with PriorityInUse.
func TestSDKCreateRuleDuplicatePriority(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	lbARN, tgARN := createLBAndTG(ctx, t, client, "dup-prio-alb", "dup-prio-tg")

	liOut, err := client.CreateListener(ctx, &elb.CreateListenerInput{
		LoadBalancerArn: aws.String(lbARN),
		Protocol:        elbtypes.ProtocolEnumHttp,
		Port:            aws.Int32(80),
		DefaultActions: []elbtypes.Action{{
			Type:           elbtypes.ActionTypeEnumForward,
			TargetGroupArn: aws.String(tgARN),
		}},
	})
	if err != nil {
		t.Fatalf("CreateListener: %v", err)
	}

	liARN := liOut.Listeners[0].ListenerArn

	mkRule := func(path string) error {
		_, err := client.CreateRule(ctx, &elb.CreateRuleInput{
			ListenerArn: liARN,
			Priority:    aws.Int32(10),
			Conditions: []elbtypes.RuleCondition{{
				Field:  aws.String("path-pattern"),
				Values: []string{path},
			}},
			Actions: []elbtypes.Action{{
				Type:           elbtypes.ActionTypeEnumForward,
				TargetGroupArn: aws.String(tgARN),
			}},
		})

		return err
	}

	if err := mkRule("/a/*"); err != nil {
		t.Fatalf("CreateRule first: %v", err)
	}

	if code := apiErrorCode(t, mkRule("/b/*")); code != "PriorityInUse" {
		t.Fatalf("CreateRule(dup priority) code = %q, want PriorityInUse", code)
	}
}

// TestSDKCreateRuleARNShape proves the rule ARN uses the "listener-rule"
// resource type with a single "arn:" prefix, never nesting the full listener
// ARN inside the value.
func TestSDKCreateRuleARNShape(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	lbARN, tgARN := createLBAndTG(ctx, t, client, "arn-shape-alb", "arn-shape-tg")

	liOut, err := client.CreateListener(ctx, &elb.CreateListenerInput{
		LoadBalancerArn: aws.String(lbARN),
		Protocol:        elbtypes.ProtocolEnumHttp,
		Port:            aws.Int32(80),
		DefaultActions: []elbtypes.Action{{
			Type:           elbtypes.ActionTypeEnumForward,
			TargetGroupArn: aws.String(tgARN),
		}},
	})
	if err != nil {
		t.Fatalf("CreateListener: %v", err)
	}

	ruleOut, err := client.CreateRule(ctx, &elb.CreateRuleInput{
		ListenerArn: liOut.Listeners[0].ListenerArn,
		Priority:    aws.Int32(5),
		Conditions: []elbtypes.RuleCondition{{
			Field:  aws.String("path-pattern"),
			Values: []string{"/z/*"},
		}},
		Actions: []elbtypes.Action{{
			Type:           elbtypes.ActionTypeEnumForward,
			TargetGroupArn: aws.String(tgARN),
		}},
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	ruleARN := aws.ToString(ruleOut.Rules[0].RuleArn)

	if !strings.Contains(ruleARN, ":listener-rule/") {
		t.Fatalf("rule ARN = %q, want resource type listener-rule/", ruleARN)
	}

	// The ARN prefix must appear exactly once: no nested arn: inside the value.
	if n := strings.Count(ruleARN, "arn:aws:"); n != 1 {
		t.Fatalf("rule ARN = %q has %d arn: prefixes, want exactly 1", ruleARN, n)
	}
}

// TestSDKDescribeListenersByARNOnly proves DescribeListeners can be called with
// ListenerArns alone (no LoadBalancerArn) and returns the named listeners,
// rather than failing with LoadBalancerNotFound.
func TestSDKDescribeListenersByARNOnly(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	lbARN, tgARN := createLBAndTG(ctx, t, client, "by-arn-alb", "by-arn-tg")

	liOut, err := client.CreateListener(ctx, &elb.CreateListenerInput{
		LoadBalancerArn: aws.String(lbARN),
		Protocol:        elbtypes.ProtocolEnumHttp,
		Port:            aws.Int32(80),
		DefaultActions: []elbtypes.Action{{
			Type:           elbtypes.ActionTypeEnumForward,
			TargetGroupArn: aws.String(tgARN),
		}},
	})
	if err != nil {
		t.Fatalf("CreateListener: %v", err)
	}

	liARN := aws.ToString(liOut.Listeners[0].ListenerArn)

	got, err := client.DescribeListeners(ctx, &elb.DescribeListenersInput{
		ListenerArns: []string{liARN},
	})
	if err != nil {
		t.Fatalf("DescribeListeners by ARN only: %v", err)
	}

	if len(got.Listeners) != 1 || aws.ToString(got.Listeners[0].ListenerArn) != liARN {
		t.Fatalf("DescribeListeners by ARN = %+v, want the created listener", got.Listeners)
	}
}
