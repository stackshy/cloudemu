package elbv2_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/smithy-go"
)

// TestSDKDeleteListenerCascadesRules proves DeleteListener removes the listener
// AND the rules under it (rules are children of the listener in real ELBv2), so
// no orphaned rule leaks. It also proves rules on a sibling listener of the same
// load balancer are untouched, and that deleting a missing listener is
// ListenerNotFound.
func TestSDKDeleteListenerCascadesRules(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	lbARN, tgARN := setupLBAndTG(t, client, "cascade-alb", "cascade-tg")

	// Two listeners on the same load balancer; only the first is deleted.
	victimARN := createForwardListener(t, client, lbARN, tgARN, 80)
	survivorARN := createForwardListener(t, client, lbARN, tgARN, 8080)

	// Two rules under the victim listener, one under the survivor.
	victimRule1 := createForwardRule(t, client, victimARN, tgARN, 10)
	victimRule2 := createForwardRule(t, client, victimARN, tgARN, 20)
	survivorRule := createForwardRule(t, client, survivorARN, tgARN, 10)

	if _, err := client.DeleteListener(ctx, &elb.DeleteListenerInput{
		ListenerArn: aws.String(victimARN),
	}); err != nil {
		t.Fatalf("DeleteListener: %v", err)
	}

	// The listener is gone from the load balancer's listing.
	listeners, err := client.DescribeListeners(ctx, &elb.DescribeListenersInput{
		LoadBalancerArn: aws.String(lbARN),
	})
	if err != nil {
		t.Fatalf("DescribeListeners: %v", err)
	}

	if len(listeners.Listeners) != 1 ||
		aws.ToString(listeners.Listeners[0].ListenerArn) != survivorARN {
		t.Fatalf("after delete listeners = %+v, want only the survivor %s",
			listeners.Listeners, survivorARN)
	}

	// DescribeRules on the deleted listener is ListenerNotFound — its rules are
	// not queryable through their (now gone) parent.
	_, err = client.DescribeRules(ctx, &elb.DescribeRulesInput{
		ListenerArn: aws.String(victimARN),
	})
	assertListenerNotFound(t, err, "DescribeRules on deleted listener")

	// The victim's rules do not leak into the surviving listener's rule listing.
	survivorRules, err := client.DescribeRules(ctx, &elb.DescribeRulesInput{
		ListenerArn: aws.String(survivorARN),
	})
	if err != nil {
		t.Fatalf("DescribeRules(survivor): %v", err)
	}

	for _, r := range survivorRules.Rules {
		arn := aws.ToString(r.RuleArn)
		if arn == victimRule1 || arn == victimRule2 {
			t.Fatalf("orphaned rule %s leaked into survivor listener", arn)
		}
	}

	// The survivor's own non-default rule is still present.
	if !rulePresent(survivorRules.Rules, survivorRule) {
		t.Fatalf("survivor rule %s missing after sibling listener delete", survivorRule)
	}

	// Deleting a listener that does not exist is ListenerNotFound.
	_, err = client.DeleteListener(ctx, &elb.DeleteListenerInput{
		ListenerArn: aws.String(victimARN),
	})
	assertListenerNotFound(t, err, "DeleteListener on missing listener")
}

func createForwardListener(t *testing.T, client *elb.Client, lbARN, tgARN string, port int32) string {
	t.Helper()

	out, err := client.CreateListener(context.Background(), &elb.CreateListenerInput{
		LoadBalancerArn: aws.String(lbARN),
		Protocol:        elbtypes.ProtocolEnumHttp,
		Port:            aws.Int32(port),
		DefaultActions: []elbtypes.Action{{
			Type:           elbtypes.ActionTypeEnumForward,
			TargetGroupArn: aws.String(tgARN),
		}},
	})
	if err != nil {
		t.Fatalf("CreateListener(port %d): %v", port, err)
	}

	return aws.ToString(out.Listeners[0].ListenerArn)
}

func createForwardRule(t *testing.T, client *elb.Client, listenerARN, tgARN string, priority int32) string {
	t.Helper()

	out, err := client.CreateRule(context.Background(), &elb.CreateRuleInput{
		ListenerArn: aws.String(listenerARN),
		Priority:    aws.Int32(priority),
		Conditions: []elbtypes.RuleCondition{{
			Field:  aws.String("path-pattern"),
			Values: []string{fmt.Sprintf("/p%d/*", priority)},
		}},
		Actions: []elbtypes.Action{{
			Type:           elbtypes.ActionTypeEnumForward,
			TargetGroupArn: aws.String(tgARN),
		}},
	})
	if err != nil {
		t.Fatalf("CreateRule(priority %d): %v", priority, err)
	}

	return aws.ToString(out.Rules[0].RuleArn)
}

func rulePresent(rules []elbtypes.Rule, arn string) bool {
	for _, r := range rules {
		if aws.ToString(r.RuleArn) == arn {
			return true
		}
	}

	return false
}

func assertListenerNotFound(t *testing.T, err error, where string) {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ListenerNotFound" {
		t.Fatalf("%s: err = %v, want ListenerNotFound", where, err)
	}
}
