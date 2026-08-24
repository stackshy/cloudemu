package iam_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/smithy-go"
)

func TestSDKCreateServiceLinkedRole(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	out, err := client.CreateServiceLinkedRole(ctx, &iam.CreateServiceLinkedRoleInput{
		AWSServiceName: aws.String("elasticbeanstalk.amazonaws.com"),
		Description:    aws.String("service-linked role for beanstalk"),
	})
	if err != nil {
		t.Fatalf("CreateServiceLinkedRole: %v", err)
	}

	// AWS's canonical service-linked-role name for elasticbeanstalk.amazonaws.com
	// is exactly AWSServiceRoleForElasticBeanstalk (a fixed per-service name, not
	// a derivable casing transform).
	name := aws.ToString(out.Role.RoleName)
	if name != "AWSServiceRoleForElasticBeanstalk" {
		t.Fatalf("role name = %q, want AWSServiceRoleForElasticBeanstalk", name)
	}

	if path := aws.ToString(out.Role.Path); path != "/aws-service-role/elasticbeanstalk.amazonaws.com/" {
		t.Fatalf("path = %q, want /aws-service-role/elasticbeanstalk.amazonaws.com/", path)
	}

	// The created role is retrievable via GetRole.
	got, err := client.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(name)})
	if err != nil {
		t.Fatalf("GetRole: %v", err)
	}

	if aws.ToString(got.Role.RoleName) != name {
		t.Fatalf("GetRole name = %q, want %q", aws.ToString(got.Role.RoleName), name)
	}

	// A CustomSuffix yields a distinct role name.
	suffixed, err := client.CreateServiceLinkedRole(ctx, &iam.CreateServiceLinkedRoleInput{
		AWSServiceName: aws.String("elasticbeanstalk.amazonaws.com"),
		CustomSuffix:   aws.String("debug"),
	})
	if err != nil {
		t.Fatalf("CreateServiceLinkedRole with suffix: %v", err)
	}

	if suffixed := aws.ToString(suffixed.Role.RoleName); suffixed == name || !strings.HasSuffix(suffixed, "_debug") {
		t.Fatalf("suffixed role name %q not distinct with _debug suffix", suffixed)
	}
}

// TestSDKCreateServiceLinkedRoleCanonicalNames pins the published names of a few
// principals whose service-linked-role names are not derivable by casing.
func TestSDKCreateServiceLinkedRoleCanonicalNames(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	cases := map[string]string{
		"ecs.amazonaws.com":                     "AWSServiceRoleForECS",
		"autoscaling.amazonaws.com":             "AWSServiceRoleForAutoScaling",
		"application-autoscaling.amazonaws.com": "AWSServiceRoleForApplicationAutoScaling",
		"elasticache.amazonaws.com":             "AWSServiceRoleForElastiCache",
	}

	for principal, want := range cases {
		out, err := client.CreateServiceLinkedRole(ctx, &iam.CreateServiceLinkedRoleInput{
			AWSServiceName: aws.String(principal),
		})
		if err != nil {
			t.Fatalf("CreateServiceLinkedRole(%s): %v", principal, err)
		}

		if got := aws.ToString(out.Role.RoleName); got != want {
			t.Fatalf("principal %s: role name = %q, want %q", principal, got, want)
		}
	}
}

// TestSDKCreateServiceLinkedRoleDuplicate asserts a duplicate service-linked-role
// name surfaces as InvalidInput (400) — EntityAlreadyExists is not in this
// action's AWS error set at all.
func TestSDKCreateServiceLinkedRoleDuplicate(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateServiceLinkedRole(ctx, &iam.CreateServiceLinkedRoleInput{
		AWSServiceName: aws.String("ecs.amazonaws.com"),
	}); err != nil {
		t.Fatalf("first CreateServiceLinkedRole: %v", err)
	}

	_, err := client.CreateServiceLinkedRole(ctx, &iam.CreateServiceLinkedRoleInput{
		AWSServiceName: aws.String("ecs.amazonaws.com"),
	})
	if err == nil {
		t.Fatal("expected duplicate CreateServiceLinkedRole to fail")
	}

	// Must be InvalidInput, never EntityAlreadyExists.
	var eae *iamtypes.EntityAlreadyExistsException
	if errors.As(err, &eae) {
		t.Fatalf("duplicate surfaced as EntityAlreadyExists, want InvalidInput: %v", err)
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidInput" {
		t.Fatalf("error code = %v, want InvalidInput", err)
	}
}
