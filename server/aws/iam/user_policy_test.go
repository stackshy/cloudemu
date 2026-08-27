package iam_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// TestSDKUserInlinePolicy exercises PutUserPolicy / GetUserPolicy /
// ListUserPolicies / DeleteUserPolicy end to end. Before the fix these actions
// returned InvalidAction, and DeleteUser could not enforce the inline-policy
// guard.
func TestSDKUserInlinePolicy(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String("u1")}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := client.PutUserPolicy(ctx, &iam.PutUserPolicyInput{
		UserName:       aws.String("u1"),
		PolicyName:     aws.String("s3access"),
		PolicyDocument: aws.String(samplePolicy),
	}); err != nil {
		t.Fatalf("PutUserPolicy: %v", err)
	}

	list, err := client.ListUserPolicies(ctx, &iam.ListUserPoliciesInput{UserName: aws.String("u1")})
	if err != nil {
		t.Fatalf("ListUserPolicies: %v", err)
	}

	if len(list.PolicyNames) != 1 || list.PolicyNames[0] != "s3access" {
		t.Fatalf("ListUserPolicies = %v", list.PolicyNames)
	}

	got, err := client.GetUserPolicy(ctx, &iam.GetUserPolicyInput{
		UserName: aws.String("u1"), PolicyName: aws.String("s3access"),
	})
	if err != nil {
		t.Fatalf("GetUserPolicy: %v", err)
	}

	if aws.ToString(got.PolicyDocument) == "" {
		t.Fatal("GetUserPolicy returned empty document")
	}

	// DeleteUser must be refused while an inline policy exists.
	_, err = client.DeleteUser(ctx, &iam.DeleteUserInput{UserName: aws.String("u1")})

	var conflict *iamtypes.DeleteConflictException
	if !errors.As(err, &conflict) {
		t.Fatalf("DeleteUser with inline policy: want DeleteConflictException, got %v", err)
	}

	if _, err := client.DeleteUserPolicy(ctx, &iam.DeleteUserPolicyInput{
		UserName: aws.String("u1"), PolicyName: aws.String("s3access"),
	}); err != nil {
		t.Fatalf("DeleteUserPolicy: %v", err)
	}

	if _, err := client.DeleteUser(ctx, &iam.DeleteUserInput{UserName: aws.String("u1")}); err != nil {
		t.Fatalf("DeleteUser after inline delete: %v", err)
	}
}
