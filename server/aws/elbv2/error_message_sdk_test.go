package elbv2_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/smithy-go"
)

// apiError extracts the smithy API error, failing the test if err is not one.
func apiError(t *testing.T, err error) smithy.APIError {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want API error, got %v", err)
	}

	return apiErr
}

// TestSDKErrorMessageHasNoCodePrefix proves the ELBv2 error <Message> carries
// only the human-readable text, never the internal cloudemu error-taxonomy name
// (e.g. "NotFound:" / "AlreadyExists:"). Real AWS never leaks such a prefix into
// its message, and a user asserting on the message text — or simply reading it —
// must not see one. The error Code still classifies the fault; only the Message
// is checked here.
func TestSDKErrorMessageHasNoCodePrefix(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	// NotFound path: describe a load balancer that does not exist.
	_, err := client.DescribeLoadBalancers(ctx, &elb.DescribeLoadBalancersInput{
		Names: []string{"does-not-exist"},
	})
	if err == nil {
		t.Fatal("DescribeLoadBalancers: want error, got nil")
	}

	apiErr := apiError(t, err)
	if apiErr.ErrorCode() != "LoadBalancerNotFound" {
		t.Errorf("code = %q, want LoadBalancerNotFound", apiErr.ErrorCode())
	}

	assertNoCodePrefix(t, apiErr.ErrorMessage())

	// AlreadyExists path: create a load balancer twice.
	name := aws.String("dup-message-lb")
	if _, err := client.CreateLoadBalancer(ctx, &elb.CreateLoadBalancerInput{
		Name: name, Subnets: []string{"subnet-a"},
	}); err != nil {
		t.Fatalf("first CreateLoadBalancer: %v", err)
	}

	_, err = client.CreateLoadBalancer(ctx, &elb.CreateLoadBalancerInput{
		Name: name, Subnets: []string{"subnet-a"},
	})
	if err == nil {
		t.Fatal("second CreateLoadBalancer: want error, got nil")
	}

	dupErr := apiError(t, err)
	if dupErr.ErrorCode() != "DuplicateLoadBalancerName" {
		t.Errorf("code = %q, want DuplicateLoadBalancerName", dupErr.ErrorCode())
	}

	assertNoCodePrefix(t, dupErr.ErrorMessage())
}

// assertNoCodePrefix fails if msg begins with a canonical cloudemu error-code
// prefix like "NotFound: " that err.Error() would prepend.
func assertNoCodePrefix(t *testing.T, msg string) {
	t.Helper()

	for _, prefix := range []string{"NotFound:", "AlreadyExists:", "InvalidArgument:", "FailedPrecondition:"} {
		if strings.HasPrefix(msg, prefix) {
			t.Errorf("message %q leaks internal code prefix %q", msg, prefix)
		}
	}
}
