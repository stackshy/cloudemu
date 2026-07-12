package elbv2_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newSDKClient(t *testing.T) *elb.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		ELB: cloud.ELB,
		// EC2 also wired so we exercise the dispatch precedence: a request for
		// ELBv2 must claim the body before EC2 sees it.
		EC2: cloud.EC2,
		VPC: cloud.VPC,
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return elb.NewFromConfig(cfg, func(o *elb.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKELBLoadBalancerLifecycle(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateLoadBalancer(ctx, &elb.CreateLoadBalancerInput{
		Name:    aws.String("my-alb"),
		Type:    elbtypes.LoadBalancerTypeEnumApplication,
		Scheme:  elbtypes.LoadBalancerSchemeEnumInternetFacing,
		Subnets: []string{"subnet-a", "subnet-b"},
		Tags:    []elbtypes.Tag{{Key: aws.String("env"), Value: aws.String("test")}},
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer: %v", err)
	}

	if len(created.LoadBalancers) != 1 {
		t.Fatalf("got %d load balancers, want 1", len(created.LoadBalancers))
	}

	lb := created.LoadBalancers[0]
	if aws.ToString(lb.LoadBalancerName) != "my-alb" {
		t.Fatalf("name = %q, want my-alb", aws.ToString(lb.LoadBalancerName))
	}

	arn := aws.ToString(lb.LoadBalancerArn)
	if arn == "" {
		t.Fatal("expected a load balancer ARN")
	}

	if lb.DNSName == nil || aws.ToString(lb.DNSName) == "" {
		t.Fatal("expected a DNS name")
	}

	// Describe by ARN.
	got, err := client.DescribeLoadBalancers(ctx, &elb.DescribeLoadBalancersInput{
		LoadBalancerArns: []string{arn},
	})
	if err != nil {
		t.Fatalf("DescribeLoadBalancers by ARN: %v", err)
	}

	if len(got.LoadBalancers) != 1 || aws.ToString(got.LoadBalancers[0].LoadBalancerArn) != arn {
		t.Fatalf("describe by ARN = %+v, want the created LB", got.LoadBalancers)
	}

	// Describe by name.
	byName, err := client.DescribeLoadBalancers(ctx, &elb.DescribeLoadBalancersInput{
		Names: []string{"my-alb"},
	})
	if err != nil {
		t.Fatalf("DescribeLoadBalancers by name: %v", err)
	}

	if len(byName.LoadBalancers) != 1 {
		t.Fatalf("describe by name got %d, want 1", len(byName.LoadBalancers))
	}

	// List all.
	all, err := client.DescribeLoadBalancers(ctx, &elb.DescribeLoadBalancersInput{})
	if err != nil {
		t.Fatalf("DescribeLoadBalancers all: %v", err)
	}

	if len(all.LoadBalancers) != 1 {
		t.Fatalf("list all got %d, want 1", len(all.LoadBalancers))
	}

	if _, err := client.DeleteLoadBalancer(ctx, &elb.DeleteLoadBalancerInput{
		LoadBalancerArn: aws.String(arn),
	}); err != nil {
		t.Fatalf("DeleteLoadBalancer: %v", err)
	}

	// After delete the ARN lookup returns nothing (driver drops the entry).
	after, err := client.DescribeLoadBalancers(ctx, &elb.DescribeLoadBalancersInput{})
	if err != nil {
		t.Fatalf("DescribeLoadBalancers after delete: %v", err)
	}

	if len(after.LoadBalancers) != 0 {
		t.Fatalf("after delete got %d LBs, want 0", len(after.LoadBalancers))
	}
}

func TestSDKELBDeleteMissingLoadBalancer(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, err := client.DeleteLoadBalancer(ctx, &elb.DeleteLoadBalancerInput{
		LoadBalancerArn: aws.String("arn:aws:elasticloadbalancing:us-east-1:000000000000:loadbalancer/missing"),
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("DeleteLoadBalancer(missing): want API error, got %v", err)
	}

	if apiErr.ErrorCode() != "LoadBalancerNotFound" {
		t.Fatalf("error code = %q, want LoadBalancerNotFound", apiErr.ErrorCode())
	}
}

func TestSDKELBTargetGroupAndTargets(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	tgOut, err := client.CreateTargetGroup(ctx, &elb.CreateTargetGroupInput{
		Name:            aws.String("web-tg"),
		Protocol:        elbtypes.ProtocolEnumHttp,
		Port:            aws.Int32(80),
		VpcId:           aws.String("vpc-123"),
		HealthCheckPath: aws.String("/healthz"),
	})
	if err != nil {
		t.Fatalf("CreateTargetGroup: %v", err)
	}

	if len(tgOut.TargetGroups) != 1 {
		t.Fatalf("got %d target groups, want 1", len(tgOut.TargetGroups))
	}

	tg := tgOut.TargetGroups[0]
	tgARN := aws.ToString(tg.TargetGroupArn)

	if aws.ToString(tg.TargetGroupName) != "web-tg" {
		t.Fatalf("tg name = %q, want web-tg", aws.ToString(tg.TargetGroupName))
	}

	if aws.ToInt32(tg.Port) != 80 {
		t.Fatalf("tg port = %d, want 80", aws.ToInt32(tg.Port))
	}

	desc, err := client.DescribeTargetGroups(ctx, &elb.DescribeTargetGroupsInput{
		Names: []string{"web-tg"},
	})
	if err != nil {
		t.Fatalf("DescribeTargetGroups: %v", err)
	}

	if len(desc.TargetGroups) != 1 {
		t.Fatalf("describe tg got %d, want 1", len(desc.TargetGroups))
	}

	// Register targets and query their health.
	if _, err := client.RegisterTargets(ctx, &elb.RegisterTargetsInput{
		TargetGroupArn: aws.String(tgARN),
		Targets: []elbtypes.TargetDescription{
			{Id: aws.String("i-111"), Port: aws.Int32(80)},
			{Id: aws.String("i-222"), Port: aws.Int32(80)},
		},
	}); err != nil {
		t.Fatalf("RegisterTargets: %v", err)
	}

	health, err := client.DescribeTargetHealth(ctx, &elb.DescribeTargetHealthInput{
		TargetGroupArn: aws.String(tgARN),
	})
	if err != nil {
		t.Fatalf("DescribeTargetHealth: %v", err)
	}

	if len(health.TargetHealthDescriptions) != 2 {
		t.Fatalf("target health got %d entries, want 2", len(health.TargetHealthDescriptions))
	}

	if _, err := client.DeregisterTargets(ctx, &elb.DeregisterTargetsInput{
		TargetGroupArn: aws.String(tgARN),
		Targets:        []elbtypes.TargetDescription{{Id: aws.String("i-111")}},
	}); err != nil {
		t.Fatalf("DeregisterTargets: %v", err)
	}

	health2, err := client.DescribeTargetHealth(ctx, &elb.DescribeTargetHealthInput{
		TargetGroupArn: aws.String(tgARN),
	})
	if err != nil {
		t.Fatalf("DescribeTargetHealth after deregister: %v", err)
	}

	if len(health2.TargetHealthDescriptions) != 1 {
		t.Fatalf("target health after deregister got %d, want 1", len(health2.TargetHealthDescriptions))
	}

	if _, err := client.DeleteTargetGroup(ctx, &elb.DeleteTargetGroupInput{
		TargetGroupArn: aws.String(tgARN),
	}); err != nil {
		t.Fatalf("DeleteTargetGroup: %v", err)
	}
}

func TestSDKELBListenerLifecycle(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	lbOut, err := client.CreateLoadBalancer(ctx, &elb.CreateLoadBalancerInput{
		Name:    aws.String("listener-alb"),
		Subnets: []string{"subnet-a"},
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer: %v", err)
	}

	lbARN := aws.ToString(lbOut.LoadBalancers[0].LoadBalancerArn)

	tgOut, err := client.CreateTargetGroup(ctx, &elb.CreateTargetGroupInput{
		Name:     aws.String("listener-tg"),
		Protocol: elbtypes.ProtocolEnumHttp,
		Port:     aws.Int32(80),
		VpcId:    aws.String("vpc-1"),
	})
	if err != nil {
		t.Fatalf("CreateTargetGroup: %v", err)
	}

	tgARN := aws.ToString(tgOut.TargetGroups[0].TargetGroupArn)

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

	if len(liOut.Listeners) != 1 {
		t.Fatalf("got %d listeners, want 1", len(liOut.Listeners))
	}

	li := liOut.Listeners[0]
	liARN := aws.ToString(li.ListenerArn)

	if aws.ToInt32(li.Port) != 80 {
		t.Fatalf("listener port = %d, want 80", aws.ToInt32(li.Port))
	}

	if len(li.DefaultActions) != 1 || aws.ToString(li.DefaultActions[0].TargetGroupArn) != tgARN {
		t.Fatalf("listener default action = %+v, want forward to %s", li.DefaultActions, tgARN)
	}

	desc, err := client.DescribeListeners(ctx, &elb.DescribeListenersInput{
		LoadBalancerArn: aws.String(lbARN),
	})
	if err != nil {
		t.Fatalf("DescribeListeners: %v", err)
	}

	if len(desc.Listeners) != 1 || aws.ToString(desc.Listeners[0].ListenerArn) != liARN {
		t.Fatalf("describe listeners = %+v, want the created listener", desc.Listeners)
	}

	if _, err := client.DeleteListener(ctx, &elb.DeleteListenerInput{
		ListenerArn: aws.String(liARN),
	}); err != nil {
		t.Fatalf("DeleteListener: %v", err)
	}
}
