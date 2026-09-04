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

// attachListener creates an application load balancer and a listener that
// forwards to tgARN, so the target group is "in use" and its targets begin
// advancing past the unused state.
func attachListener(t *testing.T, client *elb.Client, ctx context.Context, lbName, tgARN string) {
	t.Helper()

	lbOut, err := client.CreateLoadBalancer(ctx, &elb.CreateLoadBalancerInput{
		Name:    aws.String(lbName),
		Subnets: []string{"subnet-a"},
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer(%s): %v", lbName, err)
	}

	if _, err := client.CreateListener(ctx, &elb.CreateListenerInput{
		LoadBalancerArn: lbOut.LoadBalancers[0].LoadBalancerArn,
		Protocol:        elbtypes.ProtocolEnumHttp,
		Port:            aws.Int32(80),
		DefaultActions: []elbtypes.Action{{
			Type:           elbtypes.ActionTypeEnumForward,
			TargetGroupArn: aws.String(tgARN),
		}},
	}); err != nil {
		t.Fatalf("CreateListener forwarding to %s: %v", tgARN, err)
	}
}

// TestSDKLoadBalancerExtraFields locks in the Route53-alias-critical fields the
// audit found missing from Create/DescribeLoadBalancers.
func TestSDKLoadBalancerExtraFields(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	out, err := client.CreateLoadBalancer(ctx, &elb.CreateLoadBalancerInput{
		Name:           aws.String("fields-alb"),
		Subnets:        []string{"subnet-a", "subnet-b"},
		SecurityGroups: []string{"sg-111", "sg-222"},
		IpAddressType:  elbtypes.IpAddressTypeIpv4,
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer: %v", err)
	}

	lb := out.LoadBalancers[0]

	if aws.ToString(lb.CanonicalHostedZoneId) == "" {
		t.Error("CanonicalHostedZoneId is empty; Route53 alias records break without it")
	}

	if lb.CreatedTime == nil {
		t.Error("CreatedTime is nil")
	}

	if string(lb.IpAddressType) != "ipv4" {
		t.Errorf("IpAddressType = %q, want ipv4", lb.IpAddressType)
	}

	if len(lb.SecurityGroups) != 2 {
		t.Errorf("SecurityGroups = %v, want 2 entries", lb.SecurityGroups)
	}

	if len(lb.AvailabilityZones) == 0 || aws.ToString(lb.AvailabilityZones[0].ZoneName) == "" {
		t.Errorf("AvailabilityZones missing ZoneName: %+v", lb.AvailabilityZones)
	}

	// Same fields survive a Describe round-trip.
	desc, err := client.DescribeLoadBalancers(ctx, &elb.DescribeLoadBalancersInput{
		LoadBalancerArns: []string{aws.ToString(lb.LoadBalancerArn)},
	})
	if err != nil {
		t.Fatalf("DescribeLoadBalancers: %v", err)
	}

	if aws.ToString(desc.LoadBalancers[0].CanonicalHostedZoneId) == "" {
		t.Error("describe: CanonicalHostedZoneId is empty")
	}
}

// TestSDKDuplicateLoadBalancerName proves a second LB with the same name is
// rejected with the AWS-shaped code.
func TestSDKDuplicateLoadBalancerName(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	in := &elb.CreateLoadBalancerInput{Name: aws.String("dup-alb"), Subnets: []string{"subnet-a"}}
	if _, err := client.CreateLoadBalancer(ctx, in); err != nil {
		t.Fatalf("first CreateLoadBalancer: %v", err)
	}

	_, err := client.CreateLoadBalancer(ctx, in)

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "DuplicateLoadBalancerName" {
		t.Fatalf("second create: got %v, want DuplicateLoadBalancerName", err)
	}
}

// TestSDKTargetGroupHealthCheckDefaults proves the health-check fields are
// echoed with protocol-derived defaults and TargetType is honored.
func TestSDKTargetGroupHealthCheckDefaults(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	out, err := client.CreateTargetGroup(ctx, &elb.CreateTargetGroupInput{
		Name:       aws.String("hc-tg"),
		Protocol:   elbtypes.ProtocolEnumHttp,
		Port:       aws.Int32(80),
		VpcId:      aws.String("vpc-1"),
		TargetType: elbtypes.TargetTypeEnumIp,
	})
	if err != nil {
		t.Fatalf("CreateTargetGroup: %v", err)
	}

	tg := out.TargetGroups[0]

	if tg.TargetType != elbtypes.TargetTypeEnumIp {
		t.Errorf("TargetType = %q, want ip", tg.TargetType)
	}

	if tg.HealthCheckProtocol != elbtypes.ProtocolEnumHttp {
		t.Errorf("HealthCheckProtocol = %q, want HTTP", tg.HealthCheckProtocol)
	}

	if aws.ToInt32(tg.HealthCheckIntervalSeconds) != 30 {
		t.Errorf("HealthCheckIntervalSeconds = %d, want 30", aws.ToInt32(tg.HealthCheckIntervalSeconds))
	}

	if aws.ToInt32(tg.HealthyThresholdCount) != 5 {
		t.Errorf("HealthyThresholdCount = %d, want 5", aws.ToInt32(tg.HealthyThresholdCount))
	}

	if tg.Matcher == nil || aws.ToString(tg.Matcher.HttpCode) != "200" {
		t.Errorf("Matcher = %+v, want HttpCode 200", tg.Matcher)
	}
}

// TestSDKDuplicateTargetGroupName proves the target-group-specific duplicate
// code is returned (not DuplicateLoadBalancerName).
func TestSDKDuplicateTargetGroupName(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	in := &elb.CreateTargetGroupInput{
		Name:     aws.String("dup-tg"),
		Protocol: elbtypes.ProtocolEnumHttp,
		Port:     aws.Int32(80),
		VpcId:    aws.String("vpc-1"),
	}
	if _, err := client.CreateTargetGroup(ctx, in); err != nil {
		t.Fatalf("first CreateTargetGroup: %v", err)
	}

	_, err := client.CreateTargetGroup(ctx, in)

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "DuplicateTargetGroupName" {
		t.Fatalf("second create: got %v, want DuplicateTargetGroupName", err)
	}
}

// TestSDKModifyTargetGroup proves ModifyTargetGroup dispatches and applies the
// health-check update instead of returning InvalidAction.
func TestSDKModifyTargetGroup(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	tgOut, err := client.CreateTargetGroup(ctx, &elb.CreateTargetGroupInput{
		Name:     aws.String("mod-tg"),
		Protocol: elbtypes.ProtocolEnumHttp,
		Port:     aws.Int32(80),
		VpcId:    aws.String("vpc-1"),
	})
	if err != nil {
		t.Fatalf("CreateTargetGroup: %v", err)
	}

	tgARN := aws.ToString(tgOut.TargetGroups[0].TargetGroupArn)

	modOut, err := client.ModifyTargetGroup(ctx, &elb.ModifyTargetGroupInput{
		TargetGroupArn:             aws.String(tgARN),
		HealthCheckPath:            aws.String("/ping"),
		HealthCheckIntervalSeconds: aws.Int32(15),
		Matcher:                    &elbtypes.Matcher{HttpCode: aws.String("200-299")},
	})
	if err != nil {
		t.Fatalf("ModifyTargetGroup: %v", err)
	}

	got := modOut.TargetGroups[0]
	if aws.ToString(got.HealthCheckPath) != "/ping" {
		t.Errorf("HealthCheckPath = %q, want /ping", aws.ToString(got.HealthCheckPath))
	}

	if aws.ToInt32(got.HealthCheckIntervalSeconds) != 15 {
		t.Errorf("HealthCheckIntervalSeconds = %d, want 15", aws.ToInt32(got.HealthCheckIntervalSeconds))
	}

	if got.Matcher == nil || aws.ToString(got.Matcher.HttpCode) != "200-299" {
		t.Errorf("Matcher = %+v, want 200-299", got.Matcher)
	}
}

// TestSDKModifyListener proves ModifyListener dispatches and returns the updated
// listener.
func TestSDKModifyListener(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	lbARN, tgARN := setupLBAndTG(t, client, "ml-alb", "ml-tg")

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

	// The listener ARN must not nest the full load balancer ARN.
	if strings.Count(liARN, "arn:") != 1 {
		t.Errorf("listener ARN nests another arn: %q", liARN)
	}

	modOut, err := client.ModifyListener(ctx, &elb.ModifyListenerInput{
		ListenerArn: aws.String(liARN),
		Port:        aws.Int32(8080),
	})
	if err != nil {
		t.Fatalf("ModifyListener: %v", err)
	}

	if aws.ToInt32(modOut.Listeners[0].Port) != 8080 {
		t.Errorf("port = %d, want 8080", aws.ToInt32(modOut.Listeners[0].Port))
	}
}

// TestSDKModifyRuleAndPriorities proves ModifyRule and SetRulePriorities
// dispatch.
func TestSDKModifyRuleAndPriorities(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	lbARN, tgARN := setupLBAndTG(t, client, "rule-alb", "rule-tg")

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

	ruleOut, err := client.CreateRule(ctx, &elb.CreateRuleInput{
		ListenerArn: aws.String(liARN),
		Priority:    aws.Int32(10),
		Conditions: []elbtypes.RuleCondition{{
			Field:  aws.String("path-pattern"),
			Values: []string{"/a*"},
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

	if _, err := client.ModifyRule(ctx, &elb.ModifyRuleInput{
		RuleArn: aws.String(ruleARN),
		Conditions: []elbtypes.RuleCondition{{
			Field:  aws.String("path-pattern"),
			Values: []string{"/b*"},
		}},
	}); err != nil {
		t.Fatalf("ModifyRule: %v", err)
	}

	prOut, err := client.SetRulePriorities(ctx, &elb.SetRulePrioritiesInput{
		RulePriorities: []elbtypes.RulePriorityPair{{
			RuleArn:  aws.String(ruleARN),
			Priority: aws.Int32(5),
		}},
	})
	if err != nil {
		t.Fatalf("SetRulePriorities: %v", err)
	}

	if aws.ToString(prOut.Rules[0].Priority) != "5" {
		t.Errorf("priority = %q, want 5", aws.ToString(prOut.Rules[0].Priority))
	}
}

// TestSDKSetRulePrioritiesUniqueness proves SetRulePriorities rejects moving a
// rule onto a priority another rule on the same listener already holds
// (PriorityInUse), and leaves the existing priorities untouched.
func TestSDKSetRulePrioritiesUniqueness(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	lbARN, tgARN := setupLBAndTG(t, client, "prio-alb", "prio-tg")

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

	makeRule := func(priority int32) string {
		out, err := client.CreateRule(ctx, &elb.CreateRuleInput{
			ListenerArn: aws.String(liARN),
			Priority:    aws.Int32(priority),
			Conditions: []elbtypes.RuleCondition{{
				Field:  aws.String("path-pattern"),
				Values: []string{"/p*"},
			}},
			Actions: []elbtypes.Action{{
				Type:           elbtypes.ActionTypeEnumForward,
				TargetGroupArn: aws.String(tgARN),
			}},
		})
		if err != nil {
			t.Fatalf("CreateRule(%d): %v", priority, err)
		}

		return aws.ToString(out.Rules[0].RuleArn)
	}

	_ = makeRule(10)
	rule2 := makeRule(20)

	// Moving rule2 onto priority 10 collides with the first rule.
	_, err = client.SetRulePriorities(ctx, &elb.SetRulePrioritiesInput{
		RulePriorities: []elbtypes.RulePriorityPair{{
			RuleArn:  aws.String(rule2),
			Priority: aws.Int32(10),
		}},
	})
	if err == nil {
		t.Fatal("SetRulePriorities onto a used priority: want error, got nil")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "PriorityInUse" {
		t.Fatalf("SetRulePriorities error = %v, want PriorityInUse", err)
	}

	// The collision left rule2 at its original priority.
	rules, err := client.DescribeRules(ctx, &elb.DescribeRulesInput{ListenerArn: aws.String(liARN)})
	if err != nil {
		t.Fatalf("DescribeRules: %v", err)
	}

	for _, r := range rules.Rules {
		if aws.ToString(r.RuleArn) == rule2 && aws.ToString(r.Priority) != "20" {
			t.Fatalf("rule2 priority = %q, want unchanged 20", aws.ToString(r.Priority))
		}
	}
}

// TestSDKSetSecurityGroupsAndSubnets proves both network-modify ops dispatch.
func TestSDKSetSecurityGroupsAndSubnets(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	lbOut, err := client.CreateLoadBalancer(ctx, &elb.CreateLoadBalancerInput{
		Name:    aws.String("net-alb"),
		Subnets: []string{"subnet-a"},
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer: %v", err)
	}

	lbARN := aws.ToString(lbOut.LoadBalancers[0].LoadBalancerArn)

	sgOut, err := client.SetSecurityGroups(ctx, &elb.SetSecurityGroupsInput{
		LoadBalancerArn: aws.String(lbARN),
		SecurityGroups:  []string{"sg-new"},
	})
	if err != nil {
		t.Fatalf("SetSecurityGroups: %v", err)
	}

	if len(sgOut.SecurityGroupIds) != 1 || sgOut.SecurityGroupIds[0] != "sg-new" {
		t.Errorf("SecurityGroupIds = %v, want [sg-new]", sgOut.SecurityGroupIds)
	}

	subOut, err := client.SetSubnets(ctx, &elb.SetSubnetsInput{
		LoadBalancerArn: aws.String(lbARN),
		Subnets:         []string{"subnet-x", "subnet-y"},
	})
	if err != nil {
		t.Fatalf("SetSubnets: %v", err)
	}

	if len(subOut.AvailabilityZones) != 2 {
		t.Errorf("AvailabilityZones = %v, want 2", subOut.AvailabilityZones)
	}

	// The new subnets stuck.
	desc, err := client.DescribeLoadBalancers(ctx, &elb.DescribeLoadBalancersInput{
		LoadBalancerArns: []string{lbARN},
	})
	if err != nil {
		t.Fatalf("DescribeLoadBalancers: %v", err)
	}

	if len(desc.LoadBalancers[0].SecurityGroups) != 1 {
		t.Errorf("post-modify SecurityGroups = %v, want 1", desc.LoadBalancers[0].SecurityGroups)
	}
}

// TestSDKTargetHealthAdvances proves a freshly registered target reports
// "initial" (with the AWS reason code) and then advances to "healthy".
// TestSDKTargetHealthDefaultsToHealthy proves that outside AsyncSettle a
// registered target reports healthy immediately — the synchronous default
// every resource in cloudemu uses unless a caller opts into realistic
// intermediate states. See TestAsyncSettleWireELBv2TargetHealth for the
// initial->healthy->draining->removed progression under AsyncSettle.
func TestSDKTargetHealthDefaultsToHealthy(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	tgOut, err := client.CreateTargetGroup(ctx, &elb.CreateTargetGroupInput{
		Name:     aws.String("th-tg"),
		Protocol: elbtypes.ProtocolEnumHttp,
		Port:     aws.Int32(80),
		VpcId:    aws.String("vpc-1"),
	})
	if err != nil {
		t.Fatalf("CreateTargetGroup: %v", err)
	}

	tgARN := aws.ToString(tgOut.TargetGroups[0].TargetGroupArn)

	// A target group only begins health checks once a listener forwards to it.
	attachListener(t, client, ctx, "th-alb", tgARN)

	if _, err := client.RegisterTargets(ctx, &elb.RegisterTargetsInput{
		TargetGroupArn: aws.String(tgARN),
		Targets:        []elbtypes.TargetDescription{{Id: aws.String("i-1"), Port: aws.Int32(80)}},
	}); err != nil {
		t.Fatalf("RegisterTargets: %v", err)
	}

	out, err := client.DescribeTargetHealth(ctx, &elb.DescribeTargetHealthInput{
		TargetGroupArn: aws.String(tgARN),
	})
	if err != nil {
		t.Fatalf("DescribeTargetHealth: %v", err)
	}

	th := out.TargetHealthDescriptions[0].TargetHealth
	if th.State != elbtypes.TargetHealthStateEnumHealthy {
		t.Errorf("state = %q, want healthy", th.State)
	}

	// DeregisterTargets removes the target immediately, again the synchronous
	// default.
	if _, err := client.DeregisterTargets(ctx, &elb.DeregisterTargetsInput{
		TargetGroupArn: aws.String(tgARN),
		Targets:        []elbtypes.TargetDescription{{Id: aws.String("i-1"), Port: aws.Int32(80)}},
	}); err != nil {
		t.Fatalf("DeregisterTargets: %v", err)
	}

	after, err := client.DescribeTargetHealth(ctx, &elb.DescribeTargetHealthInput{
		TargetGroupArn: aws.String(tgARN),
	})
	if err != nil {
		t.Fatalf("DescribeTargetHealth after deregister: %v", err)
	}

	if n := len(after.TargetHealthDescriptions); n != 0 {
		t.Errorf("descriptions after deregister = %d, want 0", n)
	}
}

// TestSDKDescribeTargetHealthUnregisteredTarget proves DescribeTargetHealth
// with an explicit target that is not registered returns a description with
// State=unused and Reason=Target.NotRegistered (rather than silently dropping
// it), while a registered target in the same call keeps its real health. With
// no explicit Targets, only registered targets come back.
func TestSDKDescribeTargetHealthUnregisteredTarget(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	tgOut, err := client.CreateTargetGroup(ctx, &elb.CreateTargetGroupInput{
		Name:     aws.String("nr-tg"),
		Protocol: elbtypes.ProtocolEnumHttp,
		Port:     aws.Int32(80),
		VpcId:    aws.String("vpc-1"),
	})
	if err != nil {
		t.Fatalf("CreateTargetGroup: %v", err)
	}

	tgARN := aws.ToString(tgOut.TargetGroups[0].TargetGroupArn)

	// Attach a listener so the registered target advances past "unused".
	attachListener(t, client, ctx, "nr-alb", tgARN)

	if _, err := client.RegisterTargets(ctx, &elb.RegisterTargetsInput{
		TargetGroupArn: aws.String(tgARN),
		Targets:        []elbtypes.TargetDescription{{Id: aws.String("i-1"), Port: aws.Int32(80)}},
	}); err != nil {
		t.Fatalf("RegisterTargets: %v", err)
	}

	// Explicit query mixing a registered and an unregistered target.
	out, err := client.DescribeTargetHealth(ctx, &elb.DescribeTargetHealthInput{
		TargetGroupArn: aws.String(tgARN),
		Targets: []elbtypes.TargetDescription{
			{Id: aws.String("i-1"), Port: aws.Int32(80)},
			{Id: aws.String("i-missing"), Port: aws.Int32(80)},
		},
	})
	if err != nil {
		t.Fatalf("DescribeTargetHealth: %v", err)
	}

	if len(out.TargetHealthDescriptions) != 2 {
		t.Fatalf("descriptions = %d, want 2", len(out.TargetHealthDescriptions))
	}

	byID := map[string]elbtypes.TargetHealthDescription{}
	for _, d := range out.TargetHealthDescriptions {
		byID[aws.ToString(d.Target.Id)] = d
	}

	reg, ok := byID["i-1"]
	if !ok {
		t.Fatal("registered target i-1 missing from response")
	}

	if reg.TargetHealth.State != elbtypes.TargetHealthStateEnumHealthy {
		t.Errorf("registered state = %q, want healthy", reg.TargetHealth.State)
	}

	nr, ok := byID["i-missing"]
	if !ok {
		t.Fatal("unregistered target i-missing missing from response")
	}

	if nr.TargetHealth.State != elbtypes.TargetHealthStateEnumUnused {
		t.Errorf("unregistered state = %q, want unused", nr.TargetHealth.State)
	}

	if nr.TargetHealth.Reason != elbtypes.TargetHealthReasonEnumNotRegistered {
		t.Errorf("unregistered reason = %q, want Target.NotRegistered", nr.TargetHealth.Reason)
	}

	// With no explicit Targets, only the registered target is returned.
	all, err := client.DescribeTargetHealth(ctx, &elb.DescribeTargetHealthInput{
		TargetGroupArn: aws.String(tgARN),
	})
	if err != nil {
		t.Fatalf("DescribeTargetHealth (all): %v", err)
	}

	if len(all.TargetHealthDescriptions) != 1 {
		t.Fatalf("no-filter descriptions = %d, want 1", len(all.TargetHealthDescriptions))
	}

	if got := aws.ToString(all.TargetHealthDescriptions[0].Target.Id); got != "i-1" {
		t.Errorf("no-filter target = %q, want i-1", got)
	}
}

// TestSDKTargetGroupAttributes proves DescribeTargetGroupAttributes and
// ModifyTargetGroupAttributes dispatch (instead of returning InvalidAction) and
// that a modified attribute merges over the ELBv2 defaults and round-trips.
func TestSDKTargetGroupAttributes(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	tgOut, err := client.CreateTargetGroup(ctx, &elb.CreateTargetGroupInput{
		Name:     aws.String("attr-tg"),
		Protocol: elbtypes.ProtocolEnumHttp,
		Port:     aws.Int32(80),
		VpcId:    aws.String("vpc-1"),
	})
	if err != nil {
		t.Fatalf("CreateTargetGroup: %v", err)
	}

	tgARN := aws.ToString(tgOut.TargetGroups[0].TargetGroupArn)

	// Defaults are reported before any modification.
	desc, err := client.DescribeTargetGroupAttributes(ctx, &elb.DescribeTargetGroupAttributesInput{
		TargetGroupArn: aws.String(tgARN),
	})
	if err != nil {
		t.Fatalf("DescribeTargetGroupAttributes: %v", err)
	}

	if got := attrValue(desc.Attributes, "deregistration_delay.timeout_seconds"); got != "300" {
		t.Errorf("default deregistration_delay = %q, want 300", got)
	}

	// A modification merges over the defaults.
	mod, err := client.ModifyTargetGroupAttributes(ctx, &elb.ModifyTargetGroupAttributesInput{
		TargetGroupArn: aws.String(tgARN),
		Attributes: []elbtypes.TargetGroupAttribute{
			{Key: aws.String("deregistration_delay.timeout_seconds"), Value: aws.String("60")},
			{Key: aws.String("stickiness.enabled"), Value: aws.String("true")},
		},
	})
	if err != nil {
		t.Fatalf("ModifyTargetGroupAttributes: %v", err)
	}

	if got := attrValue(mod.Attributes, "deregistration_delay.timeout_seconds"); got != "60" {
		t.Errorf("modified deregistration_delay = %q, want 60", got)
	}

	if got := attrValue(mod.Attributes, "stickiness.enabled"); got != "true" {
		t.Errorf("modified stickiness.enabled = %q, want true", got)
	}

	// The change persists and an untouched default is still present.
	after, err := client.DescribeTargetGroupAttributes(ctx, &elb.DescribeTargetGroupAttributesInput{
		TargetGroupArn: aws.String(tgARN),
	})
	if err != nil {
		t.Fatalf("DescribeTargetGroupAttributes after modify: %v", err)
	}

	if got := attrValue(after.Attributes, "deregistration_delay.timeout_seconds"); got != "60" {
		t.Errorf("persisted deregistration_delay = %q, want 60", got)
	}

	if got := attrValue(after.Attributes, "load_balancing.algorithm.type"); got != "round_robin" {
		t.Errorf("untouched default load_balancing.algorithm.type = %q, want round_robin", got)
	}
}

// attrValue returns the value of the named attribute, or "" if absent.
func attrValue(attrs []elbtypes.TargetGroupAttribute, key string) string {
	for _, a := range attrs {
		if aws.ToString(a.Key) == key {
			return aws.ToString(a.Value)
		}
	}

	return ""
}

// setupLBAndTG creates a load balancer and target group, returning their ARNs.
func setupLBAndTG(t *testing.T, client *elb.Client, lbName, tgName string) (string, string) {
	t.Helper()
	ctx := context.Background()

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
