package elbv2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
)

const testCertARN = "arn:aws:acm:us-east-1:123456789012:certificate/abc"

// TestSDKTargetGroupProtocolEnum checks an unknown target group protocol is a
// ValidationError, and QUIC is accepted.
func TestSDKTargetGroupProtocolEnum(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, err := client.CreateTargetGroup(ctx, &elb.CreateTargetGroupInput{
		Name: aws.String("bogus-tg"), Protocol: "BOGUS", Port: aws.Int32(80), VpcId: aws.String("vpc-1"),
	})
	if code := apiErrorCode(t, err); code != "ValidationError" {
		t.Fatalf("BOGUS protocol: code %q, want ValidationError", code)
	}

	if _, err := client.CreateTargetGroup(ctx, &elb.CreateTargetGroupInput{
		Name: aws.String("quic-tg"), Protocol: elbtypes.ProtocolEnumQuic, Port: aws.Int32(443), VpcId: aws.String("vpc-1"),
	}); err != nil {
		t.Fatalf("QUIC target group: %v", err)
	}
}

// TestSDKListenerProtocolByLBType checks each load balancer type accepts only
// its own listener protocols, and HTTPS needs a certificate.
func TestSDKListenerProtocolByLBType(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	albARN, tgARN := createLBAndTG(ctx, t, client, "proto-alb", "proto-tg")

	nlb, err := client.CreateLoadBalancer(ctx, &elb.CreateLoadBalancerInput{
		Name: aws.String("proto-nlb"), Type: elbtypes.LoadBalancerTypeEnumNetwork, Subnets: []string{"subnet-a"},
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer nlb: %v", err)
	}

	nlbARN := aws.ToString(nlb.LoadBalancers[0].LoadBalancerArn)

	tests := []struct {
		name     string
		lbARN    string
		protocol elbtypes.ProtocolEnum
		certs    []elbtypes.Certificate
		wantErr  bool
	}{
		{name: "HTTP on NLB", lbARN: nlbARN, protocol: elbtypes.ProtocolEnumHttp, wantErr: true},
		{name: "TCP on ALB", lbARN: albARN, protocol: elbtypes.ProtocolEnumTcp, wantErr: true},
		{name: "BOGUS on ALB", lbARN: albARN, protocol: "BOGUS", wantErr: true},
		{name: "HTTPS without certificate", lbARN: albARN, protocol: elbtypes.ProtocolEnumHttps, wantErr: true},
		{name: "TLS without certificate", lbARN: nlbARN, protocol: elbtypes.ProtocolEnumTls, wantErr: true},
		{name: "HTTPS with certificate", lbARN: albARN, protocol: elbtypes.ProtocolEnumHttps,
			certs: []elbtypes.Certificate{{CertificateArn: aws.String(testCertARN)}}},
		{name: "TCP on NLB", lbARN: nlbARN, protocol: elbtypes.ProtocolEnumTcp},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.CreateListener(ctx, &elb.CreateListenerInput{
				LoadBalancerArn: aws.String(tc.lbARN),
				Protocol:        tc.protocol,
				Port:            aws.Int32(int32(8000 + i)), //nolint:gosec // small test index.
				Certificates:    tc.certs,
				DefaultActions: []elbtypes.Action{{
					Type: elbtypes.ActionTypeEnumForward, TargetGroupArn: aws.String(tgARN),
				}},
			})

			if !tc.wantErr {
				if err != nil {
					t.Fatalf("got %v, want success", err)
				}

				return
			}

			if code := apiErrorCode(t, err); code != "ValidationError" {
				t.Fatalf("code %q, want ValidationError", code)
			}
		})
	}
}

// TestSDKModifyListenerToHTTPSNeedsCertificate checks ModifyListener cannot
// switch a listener to HTTPS without a certificate, and leaves it unchanged.
func TestSDKModifyListenerToHTTPSNeedsCertificate(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	lbARN, tgARN := createLBAndTG(ctx, t, client, "mod-alb", "mod-tg")

	li, err := client.CreateListener(ctx, &elb.CreateListenerInput{
		LoadBalancerArn: aws.String(lbARN),
		Protocol:        elbtypes.ProtocolEnumHttp,
		Port:            aws.Int32(80),
		DefaultActions:  []elbtypes.Action{{Type: elbtypes.ActionTypeEnumForward, TargetGroupArn: aws.String(tgARN)}},
	})
	if err != nil {
		t.Fatalf("CreateListener: %v", err)
	}

	liARN := li.Listeners[0].ListenerArn

	_, err = client.ModifyListener(ctx, &elb.ModifyListenerInput{
		ListenerArn: liARN, Protocol: elbtypes.ProtocolEnumHttps,
	})
	if code := apiErrorCode(t, err); code != "ValidationError" {
		t.Fatalf("modify to HTTPS without cert: code %q, want ValidationError", code)
	}

	out, err := client.DescribeListeners(ctx, &elb.DescribeListenersInput{ListenerArns: []string{aws.ToString(liARN)}})
	if err != nil {
		t.Fatalf("DescribeListeners: %v", err)
	}

	if got := out.Listeners[0].Protocol; got != elbtypes.ProtocolEnumHttp {
		t.Fatalf("protocol after rejected modify = %s, want HTTP", got)
	}

	if _, err := client.ModifyListener(ctx, &elb.ModifyListenerInput{
		ListenerArn:  liARN,
		Protocol:     elbtypes.ProtocolEnumHttps,
		Port:         aws.Int32(443),
		Certificates: []elbtypes.Certificate{{CertificateArn: aws.String(testCertARN)}},
	}); err != nil {
		t.Fatalf("modify to HTTPS with cert: %v", err)
	}
}
