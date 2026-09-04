package elbv2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
)

// mkListenerOfType creates a load balancer of the given SDK type and a
// listener on it, returning the listener ARN.
func mkListenerOfType(t *testing.T, client *elb.Client, lbType elbtypes.LoadBalancerTypeEnum) string {
	t.Helper()
	ctx := context.Background()

	lbOut, err := client.CreateLoadBalancer(ctx, &elb.CreateLoadBalancerInput{
		Name:    aws.String("attr-lb-" + string(lbType)),
		Type:    lbType,
		Subnets: []string{"subnet-a"},
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer: %v", err)
	}

	tgOut, err := client.CreateTargetGroup(ctx, &elb.CreateTargetGroupInput{
		Name:     aws.String("attr-tg-" + string(lbType)),
		Protocol: elbtypes.ProtocolEnumTcp,
		Port:     aws.Int32(80),
		VpcId:    aws.String("vpc-1"),
	})
	if err != nil {
		t.Fatalf("CreateTargetGroup: %v", err)
	}

	liOut, err := client.CreateListener(ctx, &elb.CreateListenerInput{
		LoadBalancerArn: lbOut.LoadBalancers[0].LoadBalancerArn,
		Protocol:        elbtypes.ProtocolEnumTcp,
		Port:            aws.Int32(80),
		DefaultActions: []elbtypes.Action{{
			Type:           elbtypes.ActionTypeEnumForward,
			TargetGroupArn: tgOut.TargetGroups[0].TargetGroupArn,
		}},
	})
	if err != nil {
		t.Fatalf("CreateListener: %v", err)
	}

	return aws.ToString(liOut.Listeners[0].ListenerArn)
}

// TestDescribeListenerAttributesDefaults proves DescribeListenerAttributes —
// entirely unsupported before this fix — now returns the ELBv2 defaults
// derived from the parent load balancer's type: a Network Load Balancer
// listener defaults tcp.idle_timeout.seconds to 350, matching the
// ListenerAttribute API reference.
func TestDescribeListenerAttributesDefaults(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	listenerARN := mkListenerOfType(t, client, elbtypes.LoadBalancerTypeEnumNetwork)

	got, err := client.DescribeListenerAttributes(ctx,
		&elb.DescribeListenerAttributesInput{ListenerArn: aws.String(listenerARN)})
	if err != nil {
		t.Fatalf("DescribeListenerAttributes: %v", err)
	}

	attrs := map[string]string{}
	for _, a := range got.Attributes {
		attrs[aws.ToString(a.Key)] = aws.ToString(a.Value)
	}

	if attrs["tcp.idle_timeout.seconds"] != "350" {
		t.Errorf("tcp.idle_timeout.seconds = %q, want 350: %+v", attrs["tcp.idle_timeout.seconds"], got.Attributes)
	}
}

// TestModifyListenerAttributesRoundTrip proves ModifyListenerAttributes
// persists an update that DescribeListenerAttributes then reflects.
func TestModifyListenerAttributesRoundTrip(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	listenerARN := mkListenerOfType(t, client, elbtypes.LoadBalancerTypeEnumNetwork)

	modOut, err := client.ModifyListenerAttributes(ctx, &elb.ModifyListenerAttributesInput{
		ListenerArn: aws.String(listenerARN),
		Attributes: []elbtypes.ListenerAttribute{
			{Key: aws.String("tcp.idle_timeout.seconds"), Value: aws.String("60")},
		},
	})
	if err != nil {
		t.Fatalf("ModifyListenerAttributes: %v", err)
	}

	found := ""

	for _, a := range modOut.Attributes {
		if aws.ToString(a.Key) == "tcp.idle_timeout.seconds" {
			found = aws.ToString(a.Value)
		}
	}

	if found != "60" {
		t.Fatalf("ModifyListenerAttributes response tcp.idle_timeout.seconds = %q, want 60", found)
	}

	got, err := client.DescribeListenerAttributes(ctx,
		&elb.DescribeListenerAttributesInput{ListenerArn: aws.String(listenerARN)})
	if err != nil {
		t.Fatalf("DescribeListenerAttributes: %v", err)
	}

	found = ""

	for _, a := range got.Attributes {
		if aws.ToString(a.Key) == "tcp.idle_timeout.seconds" {
			found = aws.ToString(a.Value)
		}
	}

	if found != "60" {
		t.Fatalf("after modify, tcp.idle_timeout.seconds = %q, want 60", found)
	}
}
