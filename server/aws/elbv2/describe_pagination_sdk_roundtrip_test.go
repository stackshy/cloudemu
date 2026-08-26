package elbv2_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
)

// listenerTestLB creates a load balancer and returns its ARN for pagination
// fixtures.
func listenerTestLB(ctx context.Context, t *testing.T, client *elb.Client, name string) string {
	t.Helper()

	out, err := client.CreateLoadBalancer(ctx, &elb.CreateLoadBalancerInput{
		Name:    aws.String(name),
		Subnets: []string{"subnet-a"},
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer(%s): %v", name, err)
	}

	return aws.ToString(out.LoadBalancers[0].LoadBalancerArn)
}

// TestSDKDescribeListenersPagination proves DescribeListeners honors Marker and
// PageSize: a PageSize of 1 returns one listener plus a NextMarker, and walking
// the marker yields every listener exactly once before terminating.
func TestSDKDescribeListenersPagination(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	lbARN := listenerTestLB(ctx, t, client, "listeners-page-alb")

	ports := []int32{80, 81, 82}
	for _, p := range ports {
		if _, err := client.CreateListener(ctx, &elb.CreateListenerInput{
			LoadBalancerArn: aws.String(lbARN),
			Protocol:        elbtypes.ProtocolEnumHttp,
			Port:            aws.Int32(p),
			DefaultActions: []elbtypes.Action{{
				Type:                elbtypes.ActionTypeEnumFixedResponse,
				FixedResponseConfig: &elbtypes.FixedResponseActionConfig{StatusCode: aws.String("200")},
			}},
		}); err != nil {
			t.Fatalf("CreateListener(port %d): %v", p, err)
		}
	}

	seen := map[string]bool{}
	marker := ""
	pages := 0

	for {
		out, err := client.DescribeListeners(ctx, &elb.DescribeListenersInput{
			LoadBalancerArn: aws.String(lbARN),
			PageSize:        aws.Int32(1),
			Marker:          markerPtr(marker),
		})
		if err != nil {
			t.Fatalf("DescribeListeners page %d: %v", pages, err)
		}

		if len(out.Listeners) != 1 {
			t.Fatalf("page %d returned %d listeners, want 1 (PageSize ignored?)", pages, len(out.Listeners))
		}

		seen[aws.ToString(out.Listeners[0].ListenerArn)] = true
		pages++

		if out.NextMarker == nil || aws.ToString(out.NextMarker) == "" {
			break
		}

		marker = aws.ToString(out.NextMarker)

		if pages > len(ports) {
			t.Fatal("pagination did not terminate")
		}
	}

	if pages != len(ports) {
		t.Errorf("walked %d pages, want %d", pages, len(ports))
	}

	if len(seen) != len(ports) {
		t.Errorf("saw %d distinct listeners, want %d", len(seen), len(ports))
	}
}

// TestSDKDescribeListenersSinglePageNoMarker proves a page large enough to hold
// every listener returns no NextMarker.
func TestSDKDescribeListenersSinglePageNoMarker(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	lbARN := listenerTestLB(ctx, t, client, "listeners-single-alb")

	for _, p := range []int32{80, 81} {
		if _, err := client.CreateListener(ctx, &elb.CreateListenerInput{
			LoadBalancerArn: aws.String(lbARN),
			Protocol:        elbtypes.ProtocolEnumHttp,
			Port:            aws.Int32(p),
			DefaultActions: []elbtypes.Action{{
				Type:                elbtypes.ActionTypeEnumFixedResponse,
				FixedResponseConfig: &elbtypes.FixedResponseActionConfig{StatusCode: aws.String("200")},
			}},
		}); err != nil {
			t.Fatalf("CreateListener(port %d): %v", p, err)
		}
	}

	out, err := client.DescribeListeners(ctx, &elb.DescribeListenersInput{
		LoadBalancerArn: aws.String(lbARN),
	})
	if err != nil {
		t.Fatalf("DescribeListeners: %v", err)
	}

	if len(out.Listeners) != 2 {
		t.Fatalf("got %d listeners, want 2", len(out.Listeners))
	}

	if out.NextMarker != nil && aws.ToString(out.NextMarker) != "" {
		t.Errorf("single-page result carried NextMarker %q, want none", aws.ToString(out.NextMarker))
	}
}

// TestSDKDescribeListenersInvalidMarker proves an unreadable Marker is rejected.
func TestSDKDescribeListenersInvalidMarker(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	lbARN := listenerTestLB(ctx, t, client, "listeners-badmarker-alb")

	_, err := client.DescribeListeners(ctx, &elb.DescribeListenersInput{
		LoadBalancerArn: aws.String(lbARN),
		Marker:          aws.String("!!!not-base64!!!"),
	})
	if err == nil {
		t.Fatal("DescribeListeners with an invalid Marker succeeded, want an error")
	}
}

// TestSDKDescribeRulesPagination proves DescribeRules honors Marker and PageSize:
// a PageSize of 1 returns one rule plus a NextMarker, and walking the marker
// yields every rule exactly once before terminating.
func TestSDKDescribeRulesPagination(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	liARN := rulesTestListener(ctx, t, client, "rules-page-alb")

	priorities := []int32{1, 2, 3}
	for _, prio := range priorities {
		if _, err := client.CreateRule(ctx, &elb.CreateRuleInput{
			ListenerArn: aws.String(liARN),
			Priority:    aws.Int32(prio),
			Conditions: []elbtypes.RuleCondition{{
				Field:  aws.String("path-pattern"),
				Values: []string{"/p" + strings.Repeat("x", int(prio))},
			}},
			Actions: []elbtypes.Action{{
				Type:                elbtypes.ActionTypeEnumFixedResponse,
				FixedResponseConfig: &elbtypes.FixedResponseActionConfig{StatusCode: aws.String("200")},
			}},
		}); err != nil {
			t.Fatalf("CreateRule(priority %d): %v", prio, err)
		}
	}

	seen := map[string]bool{}
	marker := ""
	pages := 0

	for {
		out, err := client.DescribeRules(ctx, &elb.DescribeRulesInput{
			ListenerArn: aws.String(liARN),
			PageSize:    aws.Int32(1),
			Marker:      markerPtr(marker),
		})
		if err != nil {
			t.Fatalf("DescribeRules page %d: %v", pages, err)
		}

		if len(out.Rules) != 1 {
			t.Fatalf("page %d returned %d rules, want 1 (PageSize ignored?)", pages, len(out.Rules))
		}

		seen[aws.ToString(out.Rules[0].RuleArn)] = true
		pages++

		if out.NextMarker == nil || aws.ToString(out.NextMarker) == "" {
			break
		}

		marker = aws.ToString(out.NextMarker)

		if pages > len(priorities) {
			t.Fatal("pagination did not terminate")
		}
	}

	if pages != len(priorities) {
		t.Errorf("walked %d pages, want %d", pages, len(priorities))
	}

	if len(seen) != len(priorities) {
		t.Errorf("saw %d distinct rules, want %d", len(seen), len(priorities))
	}
}

// TestSDKDescribeRulesSinglePageNoMarker proves a page large enough to hold
// every rule returns no NextMarker.
func TestSDKDescribeRulesSinglePageNoMarker(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	liARN := rulesTestListener(ctx, t, client, "rules-single-alb")

	for _, prio := range []int32{1, 2} {
		if _, err := client.CreateRule(ctx, &elb.CreateRuleInput{
			ListenerArn: aws.String(liARN),
			Priority:    aws.Int32(prio),
			Conditions: []elbtypes.RuleCondition{{
				Field:  aws.String("path-pattern"),
				Values: []string{"/p" + strings.Repeat("x", int(prio))},
			}},
			Actions: []elbtypes.Action{{
				Type:                elbtypes.ActionTypeEnumFixedResponse,
				FixedResponseConfig: &elbtypes.FixedResponseActionConfig{StatusCode: aws.String("200")},
			}},
		}); err != nil {
			t.Fatalf("CreateRule(priority %d): %v", prio, err)
		}
	}

	out, err := client.DescribeRules(ctx, &elb.DescribeRulesInput{
		ListenerArn: aws.String(liARN),
	})
	if err != nil {
		t.Fatalf("DescribeRules: %v", err)
	}

	if len(out.Rules) != 2 {
		t.Fatalf("got %d rules, want 2", len(out.Rules))
	}

	if out.NextMarker != nil && aws.ToString(out.NextMarker) != "" {
		t.Errorf("single-page result carried NextMarker %q, want none", aws.ToString(out.NextMarker))
	}
}

// TestSDKDescribeRulesInvalidMarker proves an unreadable Marker is rejected.
func TestSDKDescribeRulesInvalidMarker(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	liARN := rulesTestListener(ctx, t, client, "rules-badmarker-alb")

	_, err := client.DescribeRules(ctx, &elb.DescribeRulesInput{
		ListenerArn: aws.String(liARN),
		Marker:      aws.String("!!!not-base64!!!"),
	})
	if err == nil {
		t.Fatal("DescribeRules with an invalid Marker succeeded, want an error")
	}
}

// rulesTestListener creates a load balancer and an HTTP listener, returning the
// listener ARN to attach rules to.
func rulesTestListener(ctx context.Context, t *testing.T, client *elb.Client, lbName string) string {
	t.Helper()

	lbARN := listenerTestLB(ctx, t, client, lbName)

	out, err := client.CreateListener(ctx, &elb.CreateListenerInput{
		LoadBalancerArn: aws.String(lbARN),
		Protocol:        elbtypes.ProtocolEnumHttp,
		Port:            aws.Int32(80),
		DefaultActions: []elbtypes.Action{{
			Type:                elbtypes.ActionTypeEnumFixedResponse,
			FixedResponseConfig: &elbtypes.FixedResponseActionConfig{StatusCode: aws.String("200")},
		}},
	})
	if err != nil {
		t.Fatalf("CreateListener: %v", err)
	}

	return aws.ToString(out.Listeners[0].ListenerArn)
}
