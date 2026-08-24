package iam_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
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

	name := aws.ToString(out.Role.RoleName)
	if !strings.HasPrefix(name, "AWSServiceRoleFor") {
		t.Fatalf("role name %q does not start with AWSServiceRoleFor", name)
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
