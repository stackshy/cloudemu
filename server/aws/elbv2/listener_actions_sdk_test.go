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

// TestSDKListenerHTTPSCertificatesAndSslPolicy proves an HTTPS listener stores
// and round-trips its SslPolicy and default certificate.
func TestSDKListenerHTTPSCertificatesAndSslPolicy(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	lbARN := createTestLBForListener(t, client, "https-alb")

	tgOut, err := client.CreateTargetGroup(ctx, &elb.CreateTargetGroupInput{
		Name:     aws.String("https-tg"),
		Protocol: elbtypes.ProtocolEnumHttps,
		Port:     aws.Int32(443),
		VpcId:    aws.String("vpc-1"),
	})
	if err != nil {
		t.Fatalf("CreateTargetGroup: %v", err)
	}

	tgARN := aws.ToString(tgOut.TargetGroups[0].TargetGroupArn)
	certARN := "arn:aws:acm:us-east-1:000000000000:certificate/abc"

	created, err := client.CreateListener(ctx, &elb.CreateListenerInput{
		LoadBalancerArn: aws.String(lbARN),
		Protocol:        elbtypes.ProtocolEnumHttps,
		Port:            aws.Int32(443),
		SslPolicy:       aws.String("ELBSecurityPolicy-2016-08"),
		Certificates:    []elbtypes.Certificate{{CertificateArn: aws.String(certARN)}},
		DefaultActions: []elbtypes.Action{{
			Type:           elbtypes.ActionTypeEnumForward,
			TargetGroupArn: aws.String(tgARN),
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

	li := desc.Listeners[0]
	if aws.ToString(li.SslPolicy) != "ELBSecurityPolicy-2016-08" {
		t.Fatalf("SslPolicy = %q, want ELBSecurityPolicy-2016-08", aws.ToString(li.SslPolicy))
	}

	if len(li.Certificates) != 1 || aws.ToString(li.Certificates[0].CertificateArn) != certARN {
		t.Fatalf("Certificates = %+v, want the default cert %s", li.Certificates, certARN)
	}

	if !aws.ToBool(li.Certificates[0].IsDefault) {
		t.Fatalf("create certificate should be the default (IsDefault=true), got %+v", li.Certificates[0])
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
