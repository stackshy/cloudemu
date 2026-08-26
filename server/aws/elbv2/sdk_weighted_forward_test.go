package elbv2_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	smithy "github.com/aws/smithy-go"
)

// mkTG is a small helper that creates a target group and returns its ARN.
func mkTG(t *testing.T, client *elb.Client, name string) string {
	t.Helper()

	out, err := client.CreateTargetGroup(context.Background(), &elb.CreateTargetGroupInput{
		Name:     aws.String(name),
		Protocol: elbtypes.ProtocolEnumHttp,
		Port:     aws.Int32(80),
		VpcId:    aws.String("vpc-1"),
	})
	if err != nil {
		t.Fatalf("CreateTargetGroup(%s): %v", name, err)
	}

	return aws.ToString(out.TargetGroups[0].TargetGroupArn)
}

// mkALB creates an application load balancer and returns its ARN.
func mkALB(t *testing.T, client *elb.Client, name string) string {
	t.Helper()

	out, err := client.CreateLoadBalancer(context.Background(), &elb.CreateLoadBalancerInput{
		Name:    aws.String(name),
		Subnets: []string{"subnet-a"},
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer(%s): %v", name, err)
	}

	return aws.ToString(out.LoadBalancers[0].LoadBalancerArn)
}

// errorCode extracts the AWS API error code from an SDK error.
func errorCode(err error) string {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		return ae.ErrorCode()
	}

	return ""
}

// TestSDKELBWeightedForwardRoundTrips proves a forward action that splits
// traffic across two weighted target groups (canary / blue-green) survives a
// create/describe cycle with both ARNs and weights intact, instead of collapsing
// to the primary group (which would show a perpetual Terraform diff).
func TestSDKELBWeightedForwardRoundTrips(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	lbARN := mkALB(t, client, "weighted-alb")
	blueARN := mkTG(t, client, "blue-tg")
	greenARN := mkTG(t, client, "green-tg")

	_, err := client.CreateListener(ctx, &elb.CreateListenerInput{
		LoadBalancerArn: aws.String(lbARN),
		Protocol:        elbtypes.ProtocolEnumHttp,
		Port:            aws.Int32(80),
		DefaultActions: []elbtypes.Action{{
			Type: elbtypes.ActionTypeEnumForward,
			ForwardConfig: &elbtypes.ForwardActionConfig{
				TargetGroups: []elbtypes.TargetGroupTuple{
					{TargetGroupArn: aws.String(blueARN), Weight: aws.Int32(90)},
					{TargetGroupArn: aws.String(greenARN), Weight: aws.Int32(10)},
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("CreateListener with weighted forward: %v", err)
	}

	desc, err := client.DescribeListeners(ctx, &elb.DescribeListenersInput{
		LoadBalancerArn: aws.String(lbARN),
	})
	if err != nil {
		t.Fatalf("DescribeListeners: %v", err)
	}

	if len(desc.Listeners) != 1 {
		t.Fatalf("got %d listeners, want 1", len(desc.Listeners))
	}

	actions := desc.Listeners[0].DefaultActions
	if len(actions) != 1 || actions[0].ForwardConfig == nil {
		t.Fatalf("default action = %+v, want a forward with ForwardConfig", actions)
	}

	groups := actions[0].ForwardConfig.TargetGroups
	if len(groups) != 2 {
		t.Fatalf("ForwardConfig groups = %d, want 2", len(groups))
	}

	weights := map[string]int32{}
	for _, g := range groups {
		weights[aws.ToString(g.TargetGroupArn)] = aws.ToInt32(g.Weight)
	}

	if weights[blueARN] != 90 || weights[greenARN] != 10 {
		t.Fatalf("weights = %v, want blue=90 green=10", weights)
	}
}

// TestSDKELBWeightedSecondaryTGInUse proves that deleting a SECONDARY weighted
// target group a listener still forwards to is rejected with ResourceInUse, so a
// live weighted target is never orphaned.
func TestSDKELBWeightedSecondaryTGInUse(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	lbARN := mkALB(t, client, "inuse-alb")
	primaryARN := mkTG(t, client, "primary-tg")
	secondaryARN := mkTG(t, client, "secondary-tg")

	if _, err := client.CreateListener(ctx, &elb.CreateListenerInput{
		LoadBalancerArn: aws.String(lbARN),
		Protocol:        elbtypes.ProtocolEnumHttp,
		Port:            aws.Int32(80),
		DefaultActions: []elbtypes.Action{{
			Type: elbtypes.ActionTypeEnumForward,
			ForwardConfig: &elbtypes.ForwardActionConfig{
				TargetGroups: []elbtypes.TargetGroupTuple{
					{TargetGroupArn: aws.String(primaryARN), Weight: aws.Int32(50)},
					{TargetGroupArn: aws.String(secondaryARN), Weight: aws.Int32(50)},
				},
			},
		}},
	}); err != nil {
		t.Fatalf("CreateListener: %v", err)
	}

	_, err := client.DeleteTargetGroup(ctx, &elb.DeleteTargetGroupInput{
		TargetGroupArn: aws.String(secondaryARN),
	})
	if err == nil {
		t.Fatalf("DeleteTargetGroup on in-use secondary weighted TG succeeded, want ResourceInUse")
	}

	if code := errorCode(err); code != "ResourceInUse" {
		t.Fatalf("DeleteTargetGroup error code = %q, want ResourceInUse", code)
	}
}

// TestSDKELBWeightedMissingSecondaryTG proves a forward action referencing a
// non-existent SECONDARY weighted target group is rejected with
// TargetGroupNotFound, not silently accepted.
func TestSDKELBWeightedMissingSecondaryTG(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	lbARN := mkALB(t, client, "missing-alb")
	realARN := mkTG(t, client, "real-tg")
	bogusARN := realARN + "-does-not-exist"

	_, err := client.CreateListener(ctx, &elb.CreateListenerInput{
		LoadBalancerArn: aws.String(lbARN),
		Protocol:        elbtypes.ProtocolEnumHttp,
		Port:            aws.Int32(80),
		DefaultActions: []elbtypes.Action{{
			Type: elbtypes.ActionTypeEnumForward,
			ForwardConfig: &elbtypes.ForwardActionConfig{
				TargetGroups: []elbtypes.TargetGroupTuple{
					{TargetGroupArn: aws.String(realARN), Weight: aws.Int32(50)},
					{TargetGroupArn: aws.String(bogusARN), Weight: aws.Int32(50)},
				},
			},
		}},
	})
	if err == nil {
		t.Fatalf("CreateListener with bogus secondary TG succeeded, want TargetGroupNotFound")
	}

	if code := errorCode(err); code != "TargetGroupNotFound" {
		t.Fatalf("CreateListener error code = %q, want TargetGroupNotFound", code)
	}
}

// TestSDKELBUnusedHealthUntilAttached proves a target group with no listener
// forwarding to it reports its targets as unused / Target.NotInUse, and only
// begins advancing to healthy once a listener routes to it.
func TestSDKELBUnusedHealthUntilAttached(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	tgARN := mkTG(t, client, "unattached-tg")

	if _, err := client.RegisterTargets(ctx, &elb.RegisterTargetsInput{
		TargetGroupArn: aws.String(tgARN),
		Targets:        []elbtypes.TargetDescription{{Id: aws.String("i-1"), Port: aws.Int32(80)}},
	}); err != nil {
		t.Fatalf("RegisterTargets: %v", err)
	}

	// No listener forwards to this TG yet: unused / Target.NotInUse, and it must
	// not advance no matter how many times it is polled.
	for i := 0; i < 3; i++ {
		health, err := client.DescribeTargetHealth(ctx, &elb.DescribeTargetHealthInput{
			TargetGroupArn: aws.String(tgARN),
		})
		if err != nil {
			t.Fatalf("DescribeTargetHealth (unattached): %v", err)
		}

		if len(health.TargetHealthDescriptions) != 1 {
			t.Fatalf("health entries = %d, want 1", len(health.TargetHealthDescriptions))
		}

		th := health.TargetHealthDescriptions[0].TargetHealth
		if th.State != elbtypes.TargetHealthStateEnumUnused {
			t.Fatalf("state = %q, want unused", th.State)
		}

		if th.Reason != elbtypes.TargetHealthReasonEnumNotInUse {
			t.Fatalf("reason = %q, want Target.NotInUse", th.Reason)
		}
	}

	// Attach a listener that forwards to the TG; now health checks begin and the
	// target settles initial -> healthy.
	lbARN := mkALB(t, client, "attach-alb")
	if _, err := client.CreateListener(ctx, &elb.CreateListenerInput{
		LoadBalancerArn: aws.String(lbARN),
		Protocol:        elbtypes.ProtocolEnumHttp,
		Port:            aws.Int32(80),
		DefaultActions: []elbtypes.Action{{
			Type:           elbtypes.ActionTypeEnumForward,
			TargetGroupArn: aws.String(tgARN),
		}},
	}); err != nil {
		t.Fatalf("CreateListener: %v", err)
	}

	// First poll after attach reports initial, next reports healthy.
	first, err := client.DescribeTargetHealth(ctx, &elb.DescribeTargetHealthInput{
		TargetGroupArn: aws.String(tgARN),
	})
	if err != nil {
		t.Fatalf("DescribeTargetHealth (attached, first): %v", err)
	}

	if s := first.TargetHealthDescriptions[0].TargetHealth.State; s != elbtypes.TargetHealthStateEnumInitial {
		t.Fatalf("first attached state = %q, want initial", s)
	}

	second, err := client.DescribeTargetHealth(ctx, &elb.DescribeTargetHealthInput{
		TargetGroupArn: aws.String(tgARN),
	})
	if err != nil {
		t.Fatalf("DescribeTargetHealth (attached, second): %v", err)
	}

	if s := second.TargetHealthDescriptions[0].TargetHealth.State; s != elbtypes.TargetHealthStateEnumHealthy {
		t.Fatalf("second attached state = %q, want healthy", s)
	}
}

// TestSDKELBSingleTGForwardStillWorks guards against regressing the single
// target-group forward path: it must still round-trip on describe and still be
// protected from deletion while a listener forwards to it.
func TestSDKELBSingleTGForwardStillWorks(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	lbARN := mkALB(t, client, "single-alb")
	tgARN := mkTG(t, client, "single-tg")

	if _, err := client.CreateListener(ctx, &elb.CreateListenerInput{
		LoadBalancerArn: aws.String(lbARN),
		Protocol:        elbtypes.ProtocolEnumHttp,
		Port:            aws.Int32(80),
		DefaultActions: []elbtypes.Action{{
			Type:           elbtypes.ActionTypeEnumForward,
			TargetGroupArn: aws.String(tgARN),
		}},
	}); err != nil {
		t.Fatalf("CreateListener: %v", err)
	}

	desc, err := client.DescribeListeners(ctx, &elb.DescribeListenersInput{
		LoadBalancerArn: aws.String(lbARN),
	})
	if err != nil {
		t.Fatalf("DescribeListeners: %v", err)
	}

	actions := desc.Listeners[0].DefaultActions
	if len(actions) != 1 || aws.ToString(actions[0].TargetGroupArn) != tgARN {
		t.Fatalf("single-TG default action = %+v, want forward to %s", actions, tgARN)
	}

	_, err = client.DeleteTargetGroup(ctx, &elb.DeleteTargetGroupInput{
		TargetGroupArn: aws.String(tgARN),
	})
	if err == nil {
		t.Fatalf("DeleteTargetGroup on in-use single TG succeeded, want ResourceInUse")
	}

	if code := errorCode(err); code != "ResourceInUse" {
		t.Fatalf("DeleteTargetGroup error code = %q, want ResourceInUse", code)
	}
}
