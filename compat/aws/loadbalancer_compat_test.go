package aws

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestAWSLoadBalancerCompat drives the ELBv2 (ALB/NLB) control plane through the
// real aws-sdk-go-v2 client. Operation names match the portable "loadbalancer"
// driver in docs/coverage/coverage.json, whose AWS native binding is ELB.
//
// GetLBAttributes maps to DescribeLoadBalancerAttributes and PutLBAttributes to
// ModifyLoadBalancerAttributes. ModifyListener and SetTargetHealth are not
// routed by the wire handler (ModifyListener has no dispatch case; SetTargetHealth
// has no AWS query-protocol action), so they are reported as gaps, not asserted.
func TestAWSLoadBalancerCompat(t *testing.T) {
	provider := cloudemu.NewAWS()
	sess := compat.BootAWS(t, awsserver.Drivers{ELB: provider.ELB})

	client := elb.NewFromConfig(sess.Config(), func(o *elb.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})
	ctx := context.Background()

	const svc = "loadbalancer"

	var (
		lbARN       string
		tgARN       string
		listenerARN string
		ruleARN     string
	)

	sess.Op(svc, "CreateLoadBalancer", func() error {
		out, err := client.CreateLoadBalancer(ctx, &elb.CreateLoadBalancerInput{
			Name:    aws.String("compat-alb"),
			Type:    elbtypes.LoadBalancerTypeEnumApplication,
			Scheme:  elbtypes.LoadBalancerSchemeEnumInternetFacing,
			Subnets: []string{"subnet-a", "subnet-b"},
		})
		if err != nil {
			return err
		}

		if len(out.LoadBalancers) != 1 {
			return fmt.Errorf("CreateLoadBalancer returned %d load balancers, want 1", len(out.LoadBalancers))
		}

		lbARN = aws.ToString(out.LoadBalancers[0].LoadBalancerArn)
		if lbARN == "" {
			return fmt.Errorf("CreateLoadBalancer returned an empty ARN")
		}

		return nil
	})

	sess.Op(svc, "DescribeLoadBalancers", func() error {
		out, err := client.DescribeLoadBalancers(ctx, &elb.DescribeLoadBalancersInput{
			LoadBalancerArns: []string{lbARN},
		})
		if err != nil {
			return err
		}

		if len(out.LoadBalancers) != 1 || aws.ToString(out.LoadBalancers[0].LoadBalancerArn) != lbARN {
			return fmt.Errorf("DescribeLoadBalancers = %+v, want the created LB", out.LoadBalancers)
		}

		return nil
	})

	sess.Op(svc, "PutLBAttributes", func() error {
		_, err := client.ModifyLoadBalancerAttributes(ctx, &elb.ModifyLoadBalancerAttributesInput{
			LoadBalancerArn: aws.String(lbARN),
			Attributes: []elbtypes.LoadBalancerAttribute{
				{Key: aws.String("idle_timeout.timeout_seconds"), Value: aws.String("120")},
			},
		})

		return err
	})

	sess.Op(svc, "GetLBAttributes", func() error {
		out, err := client.DescribeLoadBalancerAttributes(ctx, &elb.DescribeLoadBalancerAttributesInput{
			LoadBalancerArn: aws.String(lbARN),
		})
		if err != nil {
			return err
		}

		for _, a := range out.Attributes {
			if aws.ToString(a.Key) == "idle_timeout.timeout_seconds" && aws.ToString(a.Value) == "120" {
				return nil
			}
		}

		return fmt.Errorf("DescribeLoadBalancerAttributes did not return the written idle_timeout: %+v", out.Attributes)
	})

	sess.Op(svc, "CreateTargetGroup", func() error {
		out, err := client.CreateTargetGroup(ctx, &elb.CreateTargetGroupInput{
			Name:     aws.String("compat-tg"),
			Protocol: elbtypes.ProtocolEnumHttp,
			Port:     aws.Int32(80),
			VpcId:    aws.String("vpc-123"),
		})
		if err != nil {
			return err
		}

		if len(out.TargetGroups) != 1 {
			return fmt.Errorf("CreateTargetGroup returned %d groups, want 1", len(out.TargetGroups))
		}

		tgARN = aws.ToString(out.TargetGroups[0].TargetGroupArn)
		if tgARN == "" {
			return fmt.Errorf("CreateTargetGroup returned an empty ARN")
		}

		return nil
	})

	sess.Op(svc, "DescribeTargetGroups", func() error {
		out, err := client.DescribeTargetGroups(ctx, &elb.DescribeTargetGroupsInput{
			Names: []string{"compat-tg"},
		})
		if err != nil {
			return err
		}

		if len(out.TargetGroups) != 1 {
			return fmt.Errorf("DescribeTargetGroups = %d, want 1", len(out.TargetGroups))
		}

		return nil
	})

	sess.Op(svc, "RegisterTargets", func() error {
		_, err := client.RegisterTargets(ctx, &elb.RegisterTargetsInput{
			TargetGroupArn: aws.String(tgARN),
			Targets: []elbtypes.TargetDescription{
				{Id: aws.String("i-111"), Port: aws.Int32(80)},
				{Id: aws.String("i-222"), Port: aws.Int32(80)},
			},
		})

		return err
	})

	sess.Op(svc, "DescribeTargetHealth", func() error {
		out, err := client.DescribeTargetHealth(ctx, &elb.DescribeTargetHealthInput{
			TargetGroupArn: aws.String(tgARN),
		})
		if err != nil {
			return err
		}

		if len(out.TargetHealthDescriptions) != 2 {
			return fmt.Errorf("DescribeTargetHealth = %d targets, want 2", len(out.TargetHealthDescriptions))
		}

		return nil
	})

	sess.Op(svc, "DeregisterTargets", func() error {
		_, err := client.DeregisterTargets(ctx, &elb.DeregisterTargetsInput{
			TargetGroupArn: aws.String(tgARN),
			Targets:        []elbtypes.TargetDescription{{Id: aws.String("i-111")}},
		})

		return err
	})

	sess.Op(svc, "CreateListener", func() error {
		out, err := client.CreateListener(ctx, &elb.CreateListenerInput{
			LoadBalancerArn: aws.String(lbARN),
			Protocol:        elbtypes.ProtocolEnumHttp,
			Port:            aws.Int32(80),
			DefaultActions: []elbtypes.Action{{
				Type:           elbtypes.ActionTypeEnumForward,
				TargetGroupArn: aws.String(tgARN),
			}},
		})
		if err != nil {
			return err
		}

		if len(out.Listeners) != 1 {
			return fmt.Errorf("CreateListener returned %d listeners, want 1", len(out.Listeners))
		}

		listenerARN = aws.ToString(out.Listeners[0].ListenerArn)
		if listenerARN == "" {
			return fmt.Errorf("CreateListener returned an empty ARN")
		}

		return nil
	})

	sess.Op(svc, "DescribeListeners", func() error {
		out, err := client.DescribeListeners(ctx, &elb.DescribeListenersInput{
			LoadBalancerArn: aws.String(lbARN),
		})
		if err != nil {
			return err
		}

		if len(out.Listeners) != 1 || aws.ToString(out.Listeners[0].ListenerArn) != listenerARN {
			return fmt.Errorf("DescribeListeners = %+v, want the created listener", out.Listeners)
		}

		return nil
	})

	sess.Op(svc, "CreateRule", func() error {
		out, err := client.CreateRule(ctx, &elb.CreateRuleInput{
			ListenerArn: aws.String(listenerARN),
			Priority:    aws.Int32(10),
			Conditions: []elbtypes.RuleCondition{{
				Field:             aws.String("path-pattern"),
				PathPatternConfig: &elbtypes.PathPatternConditionConfig{Values: []string{"/api/*"}},
			}},
			Actions: []elbtypes.Action{{
				Type:           elbtypes.ActionTypeEnumForward,
				TargetGroupArn: aws.String(tgARN),
			}},
		})
		if err != nil {
			return err
		}

		if len(out.Rules) != 1 {
			return fmt.Errorf("CreateRule returned %d rules, want 1", len(out.Rules))
		}

		ruleARN = aws.ToString(out.Rules[0].RuleArn)
		if ruleARN == "" {
			return fmt.Errorf("CreateRule returned an empty ARN")
		}

		return nil
	})

	sess.Op(svc, "DescribeRules", func() error {
		out, err := client.DescribeRules(ctx, &elb.DescribeRulesInput{
			ListenerArn: aws.String(listenerARN),
		})
		if err != nil {
			return err
		}

		for _, ru := range out.Rules {
			if aws.ToString(ru.RuleArn) == ruleARN {
				return nil
			}
		}

		return fmt.Errorf("DescribeRules did not include the created rule %q", ruleARN)
	})

	sess.Op(svc, "DeleteRule", func() error {
		_, err := client.DeleteRule(ctx, &elb.DeleteRuleInput{
			RuleArn: aws.String(ruleARN),
		})

		return err
	})

	sess.Op(svc, "DeleteListener", func() error {
		_, err := client.DeleteListener(ctx, &elb.DeleteListenerInput{
			ListenerArn: aws.String(listenerARN),
		})

		return err
	})

	sess.Op(svc, "DeleteTargetGroup", func() error {
		_, err := client.DeleteTargetGroup(ctx, &elb.DeleteTargetGroupInput{
			TargetGroupArn: aws.String(tgARN),
		})

		return err
	})

	sess.Op(svc, "DeleteLoadBalancer", func() error {
		_, err := client.DeleteLoadBalancer(ctx, &elb.DeleteLoadBalancerInput{
			LoadBalancerArn: aws.String(lbARN),
		})

		return err
	})
}
