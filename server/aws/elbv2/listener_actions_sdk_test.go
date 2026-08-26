package elbv2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
)

func createTestLBForListener(t *testing.T, client *elb.Client, name string) string {
	t.Helper()

	out, err := client.CreateLoadBalancer(context.Background(), &elb.CreateLoadBalancerInput{
		Name:    aws.String(name),
		Subnets: []string{"subnet-a"},
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer: %v", err)
	}

	return aws.ToString(out.LoadBalancers[0].LoadBalancerArn)
}

// TestSDKListenerRedirectAction proves an HTTP listener whose only default
// action is a redirect (the HTTP->HTTPS pattern) round-trips the full redirect
// config on both CreateListener and DescribeListeners.
func TestSDKListenerRedirectAction(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	lbARN := createTestLBForListener(t, client, "redirect-alb")

	created, err := client.CreateListener(ctx, &elb.CreateListenerInput{
		LoadBalancerArn: aws.String(lbARN),
		Protocol:        elbtypes.ProtocolEnumHttp,
		Port:            aws.Int32(80),
		DefaultActions: []elbtypes.Action{{
			Type: elbtypes.ActionTypeEnumRedirect,
			RedirectConfig: &elbtypes.RedirectActionConfig{
				Protocol:   aws.String("HTTPS"),
				Port:       aws.String("443"),
				StatusCode: elbtypes.RedirectActionStatusCodeEnumHttp301,
			},
		}},
	})
	if err != nil {
		t.Fatalf("CreateListener: %v", err)
	}

	assertRedirect(t, created.Listeners[0].DefaultActions, "CreateListener")

	liARN := aws.ToString(created.Listeners[0].ListenerArn)

	desc, err := client.DescribeListeners(ctx, &elb.DescribeListenersInput{
		ListenerArns: []string{liARN},
	})
	if err != nil {
		t.Fatalf("DescribeListeners: %v", err)
	}

	assertRedirect(t, desc.Listeners[0].DefaultActions, "DescribeListeners")
}

func assertRedirect(t *testing.T, actions []elbtypes.Action, where string) {
	t.Helper()

	if len(actions) != 1 {
		t.Fatalf("%s: got %d default actions, want 1", where, len(actions))
	}

	a := actions[0]
	if a.Type != elbtypes.ActionTypeEnumRedirect {
		t.Fatalf("%s: action type = %q, want redirect", where, a.Type)
	}

	if a.RedirectConfig == nil {
		t.Fatalf("%s: redirect config dropped", where)
	}

	if aws.ToString(a.RedirectConfig.Protocol) != "HTTPS" ||
		aws.ToString(a.RedirectConfig.Port) != "443" ||
		a.RedirectConfig.StatusCode != elbtypes.RedirectActionStatusCodeEnumHttp301 {
		t.Fatalf("%s: redirect config = %+v, want HTTPS:443 HTTP_301", where, a.RedirectConfig)
	}
}

// TestSDKListenerFixedResponseAction proves a fixed-response default action
// round-trips its status code, content type and message body.
func TestSDKListenerFixedResponseAction(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	lbARN := createTestLBForListener(t, client, "fixed-alb")

	created, err := client.CreateListener(ctx, &elb.CreateListenerInput{
		LoadBalancerArn: aws.String(lbARN),
		Protocol:        elbtypes.ProtocolEnumHttp,
		Port:            aws.Int32(80),
		DefaultActions: []elbtypes.Action{{
			Type: elbtypes.ActionTypeEnumFixedResponse,
			FixedResponseConfig: &elbtypes.FixedResponseActionConfig{
				StatusCode:  aws.String("503"),
				ContentType: aws.String("text/plain"),
				MessageBody: aws.String("service unavailable"),
			},
		}},
	})
	if err != nil {
		t.Fatalf("CreateListener: %v", err)
	}

	liARN := aws.ToString(created.Listeners[0].ListenerArn)

	desc, err := client.DescribeListeners(ctx, &elb.DescribeListenersInput{
		ListenerArns: []string{liARN},
	})
	if err != nil {
		t.Fatalf("DescribeListeners: %v", err)
	}

	acts := desc.Listeners[0].DefaultActions
	if len(acts) != 1 || acts[0].Type != elbtypes.ActionTypeEnumFixedResponse {
		t.Fatalf("default actions = %+v, want one fixed-response", acts)
	}

	fr := acts[0].FixedResponseConfig
	if fr == nil || aws.ToString(fr.StatusCode) != "503" ||
		aws.ToString(fr.ContentType) != "text/plain" ||
		aws.ToString(fr.MessageBody) != "service unavailable" {
		t.Fatalf("fixed-response config = %+v, want 503/text-plain/body", fr)
	}
}
