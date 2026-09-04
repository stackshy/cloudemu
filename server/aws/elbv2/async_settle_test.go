package elbv2_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	"github.com/stackshy/cloudemu/v2"
	cloudconfig "github.com/stackshy/cloudemu/v2/config"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newAsyncELBv2 wires a full in-process AWS server with AsyncSettle enabled and
// a FakeClock the test controls, and returns a real aws-sdk-go-v2 ELBv2 client
// plus the clock. This exercises the actual wire protocol a real user hits.
func newAsyncELBv2(t *testing.T) (*elb.Client, *cloudconfig.FakeClock) {
	t.Helper()

	fc := cloudconfig.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	cloud := cloudemu.NewAWS(cloudconfig.WithClock(fc), cloudconfig.WithAsyncSettle())
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	cfg.BaseEndpoint = aws.String(ts.URL)

	return elb.NewFromConfig(cfg), fc
}

// TestAsyncSettleWireELBv2TargetHealth pins the target-health state machine a
// real SDK client sees through the wire when AsyncSettle is on: a registered
// target reports "initial" until its settle window elapses, then "healthy";
// a deregistered target reports "draining" until its own window elapses, then
// disappears from DescribeTargetHealth entirely. Both transitions are driven
// purely by the FakeClock, matching the ec2/rds/ecs async-settle wire tests.
func TestAsyncSettleWireELBv2TargetHealth(t *testing.T) {
	ctx := context.Background()
	client, fc := newAsyncELBv2(t)

	lbOut, err := client.CreateLoadBalancer(ctx, &elb.CreateLoadBalancerInput{
		Name: aws.String("async-alb"), Type: elbtypes.LoadBalancerTypeEnumApplication,
		Subnets: []string{"subnet-a"},
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer: %v", err)
	}

	tgOut, err := client.CreateTargetGroup(ctx, &elb.CreateTargetGroupInput{
		Name: aws.String("async-tg"), Protocol: elbtypes.ProtocolEnumHttp,
		Port: aws.Int32(80), VpcId: aws.String("vpc-1"),
	})
	if err != nil {
		t.Fatalf("CreateTargetGroup: %v", err)
	}

	tgARN := aws.ToString(tgOut.TargetGroups[0].TargetGroupArn)

	if _, err := client.CreateListener(ctx, &elb.CreateListenerInput{
		LoadBalancerArn: lbOut.LoadBalancers[0].LoadBalancerArn,
		Protocol:        elbtypes.ProtocolEnumHttp, Port: aws.Int32(80),
		DefaultActions: []elbtypes.Action{{
			Type: elbtypes.ActionTypeEnumForward, TargetGroupArn: aws.String(tgARN),
		}},
	}); err != nil {
		t.Fatalf("CreateListener: %v", err)
	}

	if _, err := client.RegisterTargets(ctx, &elb.RegisterTargetsInput{
		TargetGroupArn: aws.String(tgARN),
		Targets:        []elbtypes.TargetDescription{{Id: aws.String("i-1"), Port: aws.Int32(80)}},
	}); err != nil {
		t.Fatalf("RegisterTargets: %v", err)
	}

	// Registered but the settle window hasn't elapsed: initial.
	before, err := client.DescribeTargetHealth(ctx, &elb.DescribeTargetHealthInput{
		TargetGroupArn: aws.String(tgARN),
	})
	if err != nil {
		t.Fatalf("DescribeTargetHealth (before settle): %v", err)
	}

	th := before.TargetHealthDescriptions[0].TargetHealth
	if th.State != elbtypes.TargetHealthStateEnumInitial {
		t.Fatalf("state before settle = %q, want initial", th.State)
	}

	if th.Reason != elbtypes.TargetHealthReasonEnumRegistrationInProgress {
		t.Fatalf("reason before settle = %q, want Elb.RegistrationInProgress", th.Reason)
	}

	fc.Advance(3 * time.Second) // past DefaultTargetHealthSettle (2s)

	after, err := client.DescribeTargetHealth(ctx, &elb.DescribeTargetHealthInput{
		TargetGroupArn: aws.String(tgARN),
	})
	if err != nil {
		t.Fatalf("DescribeTargetHealth (after settle): %v", err)
	}

	if s := after.TargetHealthDescriptions[0].TargetHealth.State; s != elbtypes.TargetHealthStateEnumHealthy {
		t.Fatalf("state after settle = %q, want healthy", s)
	}

	// Deregister: the target must drain, not vanish immediately.
	if _, err := client.DeregisterTargets(ctx, &elb.DeregisterTargetsInput{
		TargetGroupArn: aws.String(tgARN),
		Targets:        []elbtypes.TargetDescription{{Id: aws.String("i-1"), Port: aws.Int32(80)}},
	}); err != nil {
		t.Fatalf("DeregisterTargets: %v", err)
	}

	draining, err := client.DescribeTargetHealth(ctx, &elb.DescribeTargetHealthInput{
		TargetGroupArn: aws.String(tgARN),
	})
	if err != nil {
		t.Fatalf("DescribeTargetHealth (draining): %v", err)
	}

	if len(draining.TargetHealthDescriptions) != 1 {
		t.Fatalf("draining descriptions = %d, want 1", len(draining.TargetHealthDescriptions))
	}

	dth := draining.TargetHealthDescriptions[0].TargetHealth
	if dth.State != elbtypes.TargetHealthStateEnumDraining {
		t.Fatalf("state after deregister = %q, want draining", dth.State)
	}

	if dth.Reason != elbtypes.TargetHealthReasonEnumDeregistrationInProgress {
		t.Fatalf("reason after deregister = %q, want Target.DeregistrationInProgress", dth.Reason)
	}

	fc.Advance(3 * time.Second) // past DefaultTargetDrainSettle (2s)

	removed, err := client.DescribeTargetHealth(ctx, &elb.DescribeTargetHealthInput{
		TargetGroupArn: aws.String(tgARN),
	})
	if err != nil {
		t.Fatalf("DescribeTargetHealth (after drain): %v", err)
	}

	if n := len(removed.TargetHealthDescriptions); n != 0 {
		t.Fatalf("descriptions after drain settles = %d, want 0", n)
	}
}
