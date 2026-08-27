package elbv2_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	smithy "github.com/aws/smithy-go"
)

// TestSDKELBDeletionProtectionBlocksDelete proves deletion_protection.enabled
// is honored: a protected load balancer cannot be deleted (OperationNotPermitted),
// and clearing the flag lets the delete succeed.
func TestSDKELBDeletionProtectionBlocksDelete(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateLoadBalancer(ctx, &elb.CreateLoadBalancerInput{
		Name:    aws.String("protected-alb"),
		Subnets: []string{"subnet-a"},
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer: %v", err)
	}

	arn := aws.ToString(created.LoadBalancers[0].LoadBalancerArn)

	if _, err := client.ModifyLoadBalancerAttributes(ctx, &elb.ModifyLoadBalancerAttributesInput{
		LoadBalancerArn: aws.String(arn),
		Attributes: []elbtypes.LoadBalancerAttribute{
			{Key: aws.String("deletion_protection.enabled"), Value: aws.String("true")},
		},
	}); err != nil {
		t.Fatalf("ModifyLoadBalancerAttributes(enable): %v", err)
	}

	_, err = client.DeleteLoadBalancer(ctx, &elb.DeleteLoadBalancerInput{
		LoadBalancerArn: aws.String(arn),
	})
	if err == nil {
		t.Fatal("DeleteLoadBalancer on a protected LB: want error, got nil")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "OperationNotPermitted" {
		t.Fatalf("DeleteLoadBalancer error = %v, want OperationNotPermitted", err)
	}

	// The load balancer must still exist after the rejected delete.
	if _, err := client.DescribeLoadBalancers(ctx, &elb.DescribeLoadBalancersInput{
		LoadBalancerArns: []string{arn},
	}); err != nil {
		t.Fatalf("DescribeLoadBalancers after rejected delete: %v", err)
	}

	// Clear the flag; the delete now succeeds.
	if _, err := client.ModifyLoadBalancerAttributes(ctx, &elb.ModifyLoadBalancerAttributesInput{
		LoadBalancerArn: aws.String(arn),
		Attributes: []elbtypes.LoadBalancerAttribute{
			{Key: aws.String("deletion_protection.enabled"), Value: aws.String("false")},
		},
	}); err != nil {
		t.Fatalf("ModifyLoadBalancerAttributes(disable): %v", err)
	}

	if _, err := client.DeleteLoadBalancer(ctx, &elb.DeleteLoadBalancerInput{
		LoadBalancerArn: aws.String(arn),
	}); err != nil {
		t.Fatalf("DeleteLoadBalancer after clearing protection: %v", err)
	}
}
