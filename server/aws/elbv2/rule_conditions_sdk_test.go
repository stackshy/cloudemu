package elbv2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
)

// TestSDKRuleConditionsRoundTrip proves the typed rule conditions
// (http-header, query-string, source-ip) survive CreateRule -> DescribeRules
// instead of being flattened away.
func TestSDKRuleConditionsRoundTrip(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	lbARN, tgARN := setupLBAndTG(t, client, "cond-alb", "cond-tg")

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

	createdRule, err := client.CreateRule(ctx, &elb.CreateRuleInput{
		ListenerArn: aws.String(liARN),
		Priority:    aws.Int32(10),
		Conditions: []elbtypes.RuleCondition{
			{
				Field: aws.String("http-header"),
				HttpHeaderConfig: &elbtypes.HttpHeaderConditionConfig{
					HttpHeaderName: aws.String("X-Custom"),
					Values:         []string{"v1", "v2"},
				},
			},
			{
				Field: aws.String("query-string"),
				QueryStringConfig: &elbtypes.QueryStringConditionConfig{
					Values: []elbtypes.QueryStringKeyValuePair{
						{Key: aws.String("env"), Value: aws.String("prod")},
					},
				},
			},
			{
				Field: aws.String("source-ip"),
				SourceIpConfig: &elbtypes.SourceIpConditionConfig{
					Values: []string{"10.0.0.0/8"},
				},
			},
		},
		Actions: []elbtypes.Action{{
			Type:           elbtypes.ActionTypeEnumForward,
			TargetGroupArn: aws.String(tgARN),
		}},
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	ruleARN := aws.ToString(createdRule.Rules[0].RuleArn)

	desc, err := client.DescribeRules(ctx, &elb.DescribeRulesInput{ListenerArn: aws.String(liARN)})
	if err != nil {
		t.Fatalf("DescribeRules: %v", err)
	}

	var conds []elbtypes.RuleCondition
	for _, r := range desc.Rules {
		if aws.ToString(r.RuleArn) == ruleARN {
			conds = r.Conditions
		}
	}

	byField := map[string]elbtypes.RuleCondition{}
	for _, c := range conds {
		byField[aws.ToString(c.Field)] = c
	}

	hh := byField["http-header"].HttpHeaderConfig
	if hh == nil || aws.ToString(hh.HttpHeaderName) != "X-Custom" ||
		len(hh.Values) != 2 || hh.Values[0] != "v1" {
		t.Fatalf("http-header config = %+v, want X-Custom/[v1 v2]", hh)
	}

	qs := byField["query-string"].QueryStringConfig
	if qs == nil || len(qs.Values) != 1 ||
		aws.ToString(qs.Values[0].Key) != "env" || aws.ToString(qs.Values[0].Value) != "prod" {
		t.Fatalf("query-string config = %+v, want env=prod", qs)
	}

	sip := byField["source-ip"].SourceIpConfig
	if sip == nil || len(sip.Values) != 1 || sip.Values[0] != "10.0.0.0/8" {
		t.Fatalf("source-ip config = %+v, want [10.0.0.0/8]", sip)
	}
}
