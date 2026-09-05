package elbv2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
)

// TestSDKHTTPSTargetGroupDefaultsHealthCheckToHTTP proves an HTTPS target
// group with no explicit health_check block defaults HealthCheckProtocol to
// HTTP, not HTTPS. Real ELBv2 never mirrors a target group's own protocol
// onto its health check (see the CreateTargetGroup API reference); mirroring
// it — the prior emulator behavior — surfaced as a perpetual Terraform plan
// diff on any aws_lb_target_group with protocol = "HTTPS".
func TestSDKHTTPSTargetGroupDefaultsHealthCheckToHTTP(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	out, err := client.CreateTargetGroup(ctx, &elb.CreateTargetGroupInput{
		Name:     aws.String("https-hc-tg"),
		Protocol: elbtypes.ProtocolEnumHttps,
		Port:     aws.Int32(443),
		VpcId:    aws.String("vpc-1"),
	})
	if err != nil {
		t.Fatalf("CreateTargetGroup: %v", err)
	}

	tg := out.TargetGroups[0]

	if tg.HealthCheckProtocol != elbtypes.ProtocolEnumHttp {
		t.Errorf("HealthCheckProtocol = %q, want HTTP", tg.HealthCheckProtocol)
	}

	// Survives a describe round-trip too.
	desc, err := client.DescribeTargetGroups(ctx, &elb.DescribeTargetGroupsInput{
		TargetGroupArns: []string{aws.ToString(tg.TargetGroupArn)},
	})
	if err != nil {
		t.Fatalf("DescribeTargetGroups: %v", err)
	}

	if desc.TargetGroups[0].HealthCheckProtocol != elbtypes.ProtocolEnumHttp {
		t.Errorf("describe: HealthCheckProtocol = %q, want HTTP", desc.TargetGroups[0].HealthCheckProtocol)
	}
}

// TestSDKTCPTargetGroupDefaultsHealthCheckToTCP proves a Network Load
// Balancer target group (TCP) still defaults its health check to TCP.
func TestSDKTCPTargetGroupDefaultsHealthCheckToTCP(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	out, err := client.CreateTargetGroup(ctx, &elb.CreateTargetGroupInput{
		Name:     aws.String("tcp-hc-tg"),
		Protocol: elbtypes.ProtocolEnumTcp,
		Port:     aws.Int32(80),
		VpcId:    aws.String("vpc-1"),
	})
	if err != nil {
		t.Fatalf("CreateTargetGroup: %v", err)
	}

	if out.TargetGroups[0].HealthCheckProtocol != elbtypes.ProtocolEnumTcp {
		t.Errorf("HealthCheckProtocol = %q, want TCP", out.TargetGroups[0].HealthCheckProtocol)
	}
}

// TestSDKLambdaTargetGroupHealthCheck proves a lambda target group reports
// HealthCheckEnabled=false and carries no health-check protocol — real ELBv2
// disables health checks for a lambda group by default and returns no protocol,
// so returning one makes Terraform reject the group with "health_check.protocol
// cannot be specified when target_type is lambda". The lambda-specific numeric
// defaults (interval 35, timeout 30, thresholds 5/5) are also asserted.
func TestSDKLambdaTargetGroupHealthCheck(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	out, err := client.CreateTargetGroup(ctx, &elb.CreateTargetGroupInput{
		Name:       aws.String("lambda-hc-tg"),
		TargetType: elbtypes.TargetTypeEnumLambda,
	})
	if err != nil {
		t.Fatalf("CreateTargetGroup: %v", err)
	}

	tg := out.TargetGroups[0]

	if aws.ToBool(tg.HealthCheckEnabled) {
		t.Error("HealthCheckEnabled = true, want false for a lambda target group")
	}

	if tg.HealthCheckProtocol != "" {
		t.Errorf("HealthCheckProtocol = %q, want empty for a lambda target group", tg.HealthCheckProtocol)
	}

	if got := aws.ToInt32(tg.HealthCheckIntervalSeconds); got != 35 {
		t.Errorf("HealthCheckIntervalSeconds = %d, want 35", got)
	}

	if got := aws.ToInt32(tg.HealthCheckTimeoutSeconds); got != 30 {
		t.Errorf("HealthCheckTimeoutSeconds = %d, want 30", got)
	}

	if got := aws.ToInt32(tg.HealthyThresholdCount); got != 5 {
		t.Errorf("HealthyThresholdCount = %d, want 5", got)
	}

	if got := aws.ToInt32(tg.UnhealthyThresholdCount); got != 5 {
		t.Errorf("UnhealthyThresholdCount = %d, want 5", got)
	}
}
