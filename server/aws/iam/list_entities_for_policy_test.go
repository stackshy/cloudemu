package iam_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// seedAttachedPolicy creates a managed policy and attaches it to a user, a
// group, and a role, returning the policy ARN.
func seedAttachedPolicy(t *testing.T, client *iam.Client) string {
	t.Helper()

	ctx := context.Background()

	pol, err := client.CreatePolicy(ctx, &iam.CreatePolicyInput{
		PolicyName:     aws.String("shared"),
		PolicyDocument: aws.String(samplePolicy),
	})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	arn := aws.ToString(pol.Policy.Arn)

	if _, err := client.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String("u1")}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := client.CreateGroup(ctx, &iam.CreateGroupInput{GroupName: aws.String("g1")}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if _, err := client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String("r1"),
		AssumeRolePolicyDocument: aws.String(trustPolicy),
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	if _, err := client.AttachUserPolicy(ctx, &iam.AttachUserPolicyInput{
		UserName: aws.String("u1"), PolicyArn: aws.String(arn),
	}); err != nil {
		t.Fatalf("AttachUserPolicy: %v", err)
	}
	if _, err := client.AttachGroupPolicy(ctx, &iam.AttachGroupPolicyInput{
		GroupName: aws.String("g1"), PolicyArn: aws.String(arn),
	}); err != nil {
		t.Fatalf("AttachGroupPolicy: %v", err)
	}
	if _, err := client.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
		RoleName: aws.String("r1"), PolicyArn: aws.String(arn),
	}); err != nil {
		t.Fatalf("AttachRolePolicy: %v", err)
	}

	return arn
}

func TestSDKListEntitiesForPolicy(t *testing.T) {
	client := newSDKClient(t)
	arn := seedAttachedPolicy(t, client)

	out, err := client.ListEntitiesForPolicy(context.Background(), &iam.ListEntitiesForPolicyInput{
		PolicyArn: aws.String(arn),
	})
	if err != nil {
		t.Fatalf("ListEntitiesForPolicy: %v", err)
	}

	if n := len(out.PolicyUsers); n != 1 || aws.ToString(out.PolicyUsers[0].UserName) != "u1" {
		t.Fatalf("PolicyUsers = %+v, want [u1]", out.PolicyUsers)
	}
	if n := len(out.PolicyGroups); n != 1 || aws.ToString(out.PolicyGroups[0].GroupName) != "g1" {
		t.Fatalf("PolicyGroups = %+v, want [g1]", out.PolicyGroups)
	}
	if n := len(out.PolicyRoles); n != 1 || aws.ToString(out.PolicyRoles[0].RoleName) != "r1" {
		t.Fatalf("PolicyRoles = %+v, want [r1]", out.PolicyRoles)
	}

	// IDs must be populated (Name+Id shape).
	if aws.ToString(out.PolicyUsers[0].UserId) == "" {
		t.Fatalf("PolicyUsers[0].UserId is empty")
	}
}

func TestSDKListEntitiesForPolicyEntityFilter(t *testing.T) {
	client := newSDKClient(t)
	arn := seedAttachedPolicy(t, client)

	out, err := client.ListEntitiesForPolicy(context.Background(), &iam.ListEntitiesForPolicyInput{
		PolicyArn:    aws.String(arn),
		EntityFilter: iamtypes.EntityTypeUser,
	})
	if err != nil {
		t.Fatalf("ListEntitiesForPolicy(EntityFilter=User): %v", err)
	}

	if len(out.PolicyUsers) != 1 {
		t.Fatalf("PolicyUsers = %+v, want 1", out.PolicyUsers)
	}
	if len(out.PolicyGroups) != 0 || len(out.PolicyRoles) != 0 {
		t.Fatalf("EntityFilter=User leaked groups/roles: groups=%d roles=%d",
			len(out.PolicyGroups), len(out.PolicyRoles))
	}
}

func TestSDKListEntitiesForPolicyPaginates(t *testing.T) {
	client := newSDKClient(t)
	arn := seedAttachedPolicy(t, client)
	ctx := context.Background()

	first, err := client.ListEntitiesForPolicy(ctx, &iam.ListEntitiesForPolicyInput{
		PolicyArn: aws.String(arn),
		MaxItems:  aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("ListEntitiesForPolicy(MaxItems=2): %v", err)
	}

	got := len(first.PolicyUsers) + len(first.PolicyGroups) + len(first.PolicyRoles)
	if got != 2 {
		t.Fatalf("first page returned %d entities, want 2", got)
	}
	if !first.IsTruncated || aws.ToString(first.Marker) == "" {
		t.Fatalf("first page IsTruncated=%v Marker=%q, want truncated with marker",
			first.IsTruncated, aws.ToString(first.Marker))
	}

	second, err := client.ListEntitiesForPolicy(ctx, &iam.ListEntitiesForPolicyInput{
		PolicyArn: aws.String(arn),
		Marker:    first.Marker,
	})
	if err != nil {
		t.Fatalf("ListEntitiesForPolicy(page 2): %v", err)
	}

	got2 := len(second.PolicyUsers) + len(second.PolicyGroups) + len(second.PolicyRoles)
	if got2 != 1 {
		t.Fatalf("second page returned %d entities, want 1", got2)
	}
	if second.IsTruncated {
		t.Fatalf("second page should not be truncated")
	}
}
