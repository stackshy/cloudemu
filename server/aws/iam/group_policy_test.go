package iam_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// TestSDKGroupManagedPolicy exercises AttachGroupPolicy /
// ListAttachedGroupPolicies / DetachGroupPolicy end to end. Before the fix
// these actions returned InvalidAction.
func TestSDKGroupManagedPolicy(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateGroup(ctx, &iam.CreateGroupInput{GroupName: aws.String("g1")}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	const arn = "arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess"

	if _, err := client.AttachGroupPolicy(ctx, &iam.AttachGroupPolicyInput{
		GroupName: aws.String("g1"), PolicyArn: aws.String(arn),
	}); err != nil {
		t.Fatalf("AttachGroupPolicy: %v", err)
	}

	list, err := client.ListAttachedGroupPolicies(ctx, &iam.ListAttachedGroupPoliciesInput{
		GroupName: aws.String("g1"),
	})
	if err != nil {
		t.Fatalf("ListAttachedGroupPolicies: %v", err)
	}

	if len(list.AttachedPolicies) != 1 || aws.ToString(list.AttachedPolicies[0].PolicyArn) != arn {
		t.Fatalf("ListAttachedGroupPolicies = %v", list.AttachedPolicies)
	}

	// DeleteGroup must be refused while a managed policy is attached.
	_, err = client.DeleteGroup(ctx, &iam.DeleteGroupInput{GroupName: aws.String("g1")})

	var conflict *iamtypes.DeleteConflictException
	if !errors.As(err, &conflict) {
		t.Fatalf("DeleteGroup with attached policy: want DeleteConflictException, got %v", err)
	}

	if _, err := client.DetachGroupPolicy(ctx, &iam.DetachGroupPolicyInput{
		GroupName: aws.String("g1"), PolicyArn: aws.String(arn),
	}); err != nil {
		t.Fatalf("DetachGroupPolicy: %v", err)
	}

	if _, err := client.DeleteGroup(ctx, &iam.DeleteGroupInput{GroupName: aws.String("g1")}); err != nil {
		t.Fatalf("DeleteGroup after detach: %v", err)
	}
}

// TestSDKGroupInlinePolicy exercises PutGroupPolicy / GetGroupPolicy /
// ListGroupPolicies / DeleteGroupPolicy end to end.
func TestSDKGroupInlinePolicy(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateGroup(ctx, &iam.CreateGroupInput{GroupName: aws.String("g1")}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	if _, err := client.PutGroupPolicy(ctx, &iam.PutGroupPolicyInput{
		GroupName:      aws.String("g1"),
		PolicyName:     aws.String("s3access"),
		PolicyDocument: aws.String(samplePolicy),
	}); err != nil {
		t.Fatalf("PutGroupPolicy: %v", err)
	}

	list, err := client.ListGroupPolicies(ctx, &iam.ListGroupPoliciesInput{GroupName: aws.String("g1")})
	if err != nil {
		t.Fatalf("ListGroupPolicies: %v", err)
	}

	if len(list.PolicyNames) != 1 || list.PolicyNames[0] != "s3access" {
		t.Fatalf("ListGroupPolicies = %v", list.PolicyNames)
	}

	got, err := client.GetGroupPolicy(ctx, &iam.GetGroupPolicyInput{
		GroupName: aws.String("g1"), PolicyName: aws.String("s3access"),
	})
	if err != nil {
		t.Fatalf("GetGroupPolicy: %v", err)
	}

	if aws.ToString(got.PolicyDocument) == "" {
		t.Fatal("GetGroupPolicy returned empty document")
	}

	// DeleteGroup must be refused while an inline policy exists.
	_, err = client.DeleteGroup(ctx, &iam.DeleteGroupInput{GroupName: aws.String("g1")})

	var conflict *iamtypes.DeleteConflictException
	if !errors.As(err, &conflict) {
		t.Fatalf("DeleteGroup with inline policy: want DeleteConflictException, got %v", err)
	}

	if _, err := client.DeleteGroupPolicy(ctx, &iam.DeleteGroupPolicyInput{
		GroupName: aws.String("g1"), PolicyName: aws.String("s3access"),
	}); err != nil {
		t.Fatalf("DeleteGroupPolicy: %v", err)
	}

	if _, err := client.DeleteGroup(ctx, &iam.DeleteGroupInput{GroupName: aws.String("g1")}); err != nil {
		t.Fatalf("DeleteGroup after inline delete: %v", err)
	}
}
