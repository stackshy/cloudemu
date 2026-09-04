package elbv2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
)

// mkListenerWithRule creates a load balancer, target group, listener (with
// Tags), and a rule on that listener (with Tags), returning their ARNs.
func mkListenerWithRule(t *testing.T, client *elb.Client) (listenerARN, ruleARN string) {
	t.Helper()
	ctx := context.Background()

	lbARN, tgARN := setupLBAndTG(t, client, "tag-lb", "tag-tg")

	liOut, err := client.CreateListener(ctx, &elb.CreateListenerInput{
		LoadBalancerArn: aws.String(lbARN),
		Protocol:        elbtypes.ProtocolEnumHttp,
		Port:            aws.Int32(80),
		DefaultActions: []elbtypes.Action{{
			Type:           elbtypes.ActionTypeEnumForward,
			TargetGroupArn: aws.String(tgARN),
		}},
		Tags: []elbtypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	})
	if err != nil {
		t.Fatalf("CreateListener: %v", err)
	}

	listenerARN = aws.ToString(liOut.Listeners[0].ListenerArn)

	ruleOut, err := client.CreateRule(ctx, &elb.CreateRuleInput{
		ListenerArn: aws.String(listenerARN),
		Priority:    aws.Int32(10),
		Conditions: []elbtypes.RuleCondition{{
			Field: aws.String("path-pattern"),
			PathPatternConfig: &elbtypes.PathPatternConditionConfig{
				Values: []string{"/api/*"},
			},
		}},
		Actions: []elbtypes.Action{{
			Type:           elbtypes.ActionTypeEnumForward,
			TargetGroupArn: aws.String(tgARN),
		}},
		Tags: []elbtypes.Tag{{Key: aws.String("team"), Value: aws.String("platform")}},
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	ruleARN = aws.ToString(ruleOut.Rules[0].RuleArn)

	return listenerARN, ruleARN
}

// TestDescribeTagsResolvesListenerTags proves DescribeTags called with a
// listener ARN returns the tags supplied at CreateListener time. Before this
// fix ListenerInfo carried no Tags field at all, so a listener's tags were
// dropped at create time and DescribeTags reported ListenerNotFound for its
// ARN — breaking Terraform's aws_lb_listener resource, whose Read always
// fetches tags after create/refresh.
func TestDescribeTagsResolvesListenerTags(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	listenerARN, _ := mkListenerWithRule(t, client)

	got, err := client.DescribeTags(ctx, &elb.DescribeTagsInput{ResourceArns: []string{listenerARN}})
	if err != nil {
		t.Fatalf("DescribeTags(listener): %v", err)
	}

	if len(got.TagDescriptions) != 1 {
		t.Fatalf("tag descriptions = %d, want 1", len(got.TagDescriptions))
	}

	found := false

	for _, tag := range got.TagDescriptions[0].Tags {
		if aws.ToString(tag.Key) == "env" && aws.ToString(tag.Value) == "prod" {
			found = true
		}
	}

	if !found {
		t.Errorf("listener tags not returned: %+v", got.TagDescriptions[0].Tags)
	}
}

// TestDescribeTagsResolvesRuleTags proves DescribeTags called with a listener
// rule's ARN returns the tags supplied at CreateRule time. DescribeRules only
// looks up by parent ListenerArn, so this exercises the new RuleGetter path
// DescribeTags falls back to for a rule ARN.
func TestDescribeTagsResolvesRuleTags(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, ruleARN := mkListenerWithRule(t, client)

	got, err := client.DescribeTags(ctx, &elb.DescribeTagsInput{ResourceArns: []string{ruleARN}})
	if err != nil {
		t.Fatalf("DescribeTags(rule): %v", err)
	}

	if len(got.TagDescriptions) != 1 {
		t.Fatalf("tag descriptions = %d, want 1", len(got.TagDescriptions))
	}

	found := false

	for _, tag := range got.TagDescriptions[0].Tags {
		if aws.ToString(tag.Key) == "team" && aws.ToString(tag.Value) == "platform" {
			found = true
		}
	}

	if !found {
		t.Errorf("rule tags not returned: %+v", got.TagDescriptions[0].Tags)
	}
}

// TestAddAndRemoveTagsOnListenerAndRule proves AddTags/RemoveTags — the
// mutating counterpart to DescribeTags — also reach listener and rule ARNs,
// not just load balancers and target groups.
func TestAddAndRemoveTagsOnListenerAndRule(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	listenerARN, ruleARN := mkListenerWithRule(t, client)

	if _, err := client.AddTags(ctx, &elb.AddTagsInput{
		ResourceArns: []string{listenerARN, ruleARN},
		Tags:         []elbtypes.Tag{{Key: aws.String("owner"), Value: aws.String("team-a")}},
	}); err != nil {
		t.Fatalf("AddTags: %v", err)
	}

	got, err := client.DescribeTags(ctx, &elb.DescribeTagsInput{ResourceArns: []string{listenerARN, ruleARN}})
	if err != nil {
		t.Fatalf("DescribeTags after AddTags: %v", err)
	}

	byARN := map[string]map[string]string{}

	for _, td := range got.TagDescriptions {
		m := map[string]string{}
		for _, tag := range td.Tags {
			m[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
		}

		byARN[aws.ToString(td.ResourceArn)] = m
	}

	if byARN[listenerARN]["owner"] != "team-a" {
		t.Errorf("listener owner tag = %q, want team-a", byARN[listenerARN]["owner"])
	}

	if byARN[ruleARN]["owner"] != "team-a" {
		t.Errorf("rule owner tag = %q, want team-a", byARN[ruleARN]["owner"])
	}

	if _, err := client.RemoveTags(ctx, &elb.RemoveTagsInput{
		ResourceArns: []string{listenerARN, ruleARN}, TagKeys: []string{"owner"},
	}); err != nil {
		t.Fatalf("RemoveTags: %v", err)
	}

	got, err = client.DescribeTags(ctx, &elb.DescribeTagsInput{ResourceArns: []string{listenerARN, ruleARN}})
	if err != nil {
		t.Fatalf("DescribeTags after RemoveTags: %v", err)
	}

	for _, td := range got.TagDescriptions {
		for _, tag := range td.Tags {
			if aws.ToString(tag.Key) == "owner" {
				t.Errorf("owner tag survived RemoveTags on %s", aws.ToString(td.ResourceArn))
			}
		}
	}
}

// TestDescribeRulesByRuleArnsWithoutListenerArn proves DescribeRules resolves
// rules directly by RuleArns when called without a ListenerArn — the shape
// Terraform's aws_lb_listener_rule data source and resource refresh use.
// Before this fix an empty ListenerArn fell through to the listener-exists
// check and errored instead of resolving the requested rules.
func TestDescribeRulesByRuleArnsWithoutListenerArn(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, ruleARN := mkListenerWithRule(t, client)

	got, err := client.DescribeRules(ctx, &elb.DescribeRulesInput{RuleArns: []string{ruleARN}})
	if err != nil {
		t.Fatalf("DescribeRules(RuleArns only): %v", err)
	}

	if len(got.Rules) != 1 || aws.ToString(got.Rules[0].RuleArn) != ruleARN {
		t.Fatalf("DescribeRules(RuleArns) = %+v, want [%s]", got.Rules, ruleARN)
	}
}

// TestDescribeRulesByUnknownRuleArn proves an unknown rule ARN still reports a
// proper error rather than an empty success.
func TestDescribeRulesByUnknownRuleArn(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, err := client.DescribeRules(ctx, &elb.DescribeRulesInput{RuleArns: []string{"arn:nope"}})
	if err == nil {
		t.Fatal("DescribeRules(unknown RuleArn) = nil error, want an error")
	}
}
