package iam_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// TestSDKRoleDescriptionRoundTrips proves CreateRole persists Description and
// GetRole echoes it (previously dropped — GetRole returned "").
func TestSDKRoleDescriptionRoundTrips(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String("described-role"),
		AssumeRolePolicyDocument: aws.String(trustPolicy),
		Description:              aws.String("my role"),
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	got, err := client.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String("described-role")})
	if err != nil {
		t.Fatalf("GetRole: %v", err)
	}

	if desc := aws.ToString(got.Role.Description); desc != "my role" {
		t.Fatalf("Role.Description = %q, want %q", desc, "my role")
	}
}

// TestSDKDeleteRoleConflictWhenPolicyAttached proves DeleteRole is refused with
// DeleteConflictException while a managed policy is still attached, and
// succeeds once detached.
func TestSDKDeleteRoleConflictWhenPolicyAttached(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String("busy-role"),
		AssumeRolePolicyDocument: aws.String(trustPolicy),
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	policy, err := client.CreatePolicy(ctx, &iam.CreatePolicyInput{
		PolicyName:     aws.String("attached-pol"),
		PolicyDocument: aws.String(samplePolicy),
	})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	arn := aws.ToString(policy.Policy.Arn)
	if _, err := client.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
		RoleName: aws.String("busy-role"), PolicyArn: aws.String(arn),
	}); err != nil {
		t.Fatalf("AttachRolePolicy: %v", err)
	}

	_, err = client.DeleteRole(ctx, &iam.DeleteRoleInput{RoleName: aws.String("busy-role")})

	var conflict *iamtypes.DeleteConflictException
	if !errors.As(err, &conflict) {
		t.Fatalf("DeleteRole while attached: want DeleteConflictException, got %v", err)
	}

	if _, err := client.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
		RoleName: aws.String("busy-role"), PolicyArn: aws.String(arn),
	}); err != nil {
		t.Fatalf("DetachRolePolicy: %v", err)
	}

	if _, err := client.DeleteRole(ctx, &iam.DeleteRoleInput{RoleName: aws.String("busy-role")}); err != nil {
		t.Fatalf("DeleteRole after detach: %v", err)
	}
}

// TestSDKDeleteUserConflictWhenPolicyAttached proves DeleteUser is refused with
// DeleteConflictException while a managed policy is still attached, and succeeds
// once detached.
func TestSDKDeleteUserConflictWhenPolicyAttached(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String("busy-user")}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	policy, err := client.CreatePolicy(ctx, &iam.CreatePolicyInput{
		PolicyName:     aws.String("user-pol"),
		PolicyDocument: aws.String(samplePolicy),
	})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	arn := aws.ToString(policy.Policy.Arn)
	if _, err := client.AttachUserPolicy(ctx, &iam.AttachUserPolicyInput{
		UserName: aws.String("busy-user"), PolicyArn: aws.String(arn),
	}); err != nil {
		t.Fatalf("AttachUserPolicy: %v", err)
	}

	_, err = client.DeleteUser(ctx, &iam.DeleteUserInput{UserName: aws.String("busy-user")})

	var conflict *iamtypes.DeleteConflictException
	if !errors.As(err, &conflict) {
		t.Fatalf("DeleteUser while attached: want DeleteConflictException, got %v", err)
	}

	if _, err := client.DetachUserPolicy(ctx, &iam.DetachUserPolicyInput{
		UserName: aws.String("busy-user"), PolicyArn: aws.String(arn),
	}); err != nil {
		t.Fatalf("DetachUserPolicy: %v", err)
	}

	if _, err := client.DeleteUser(ctx, &iam.DeleteUserInput{UserName: aws.String("busy-user")}); err != nil {
		t.Fatalf("DeleteUser after detach: %v", err)
	}
}

// TestSDKDeletePolicyConflictWhenAttached proves DeletePolicy is refused with
// DeleteConflictException while it is still attached to a role, and succeeds
// once detached.
func TestSDKDeletePolicyConflictWhenAttached(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String("holder-role"),
		AssumeRolePolicyDocument: aws.String(trustPolicy),
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	policy, err := client.CreatePolicy(ctx, &iam.CreatePolicyInput{
		PolicyName:     aws.String("held-pol"),
		PolicyDocument: aws.String(samplePolicy),
	})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	arn := aws.ToString(policy.Policy.Arn)
	if _, err := client.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
		RoleName: aws.String("holder-role"), PolicyArn: aws.String(arn),
	}); err != nil {
		t.Fatalf("AttachRolePolicy: %v", err)
	}

	_, err = client.DeletePolicy(ctx, &iam.DeletePolicyInput{PolicyArn: aws.String(arn)})

	var conflict *iamtypes.DeleteConflictException
	if !errors.As(err, &conflict) {
		t.Fatalf("DeletePolicy while attached: want DeleteConflictException, got %v", err)
	}

	if _, err := client.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
		RoleName: aws.String("holder-role"), PolicyArn: aws.String(arn),
	}); err != nil {
		t.Fatalf("DetachRolePolicy: %v", err)
	}

	if _, err := client.DeletePolicy(ctx, &iam.DeletePolicyInput{PolicyArn: aws.String(arn)}); err != nil {
		t.Fatalf("DeletePolicy after detach: %v", err)
	}
}

// TestSDKGetUserNoUserNameReturnsCaller proves GetUser with no UserName returns
// the calling principal's own user record instead of NoSuchEntity.
func TestSDKGetUserNoUserNameReturnsCaller(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	got, err := client.GetUser(ctx, &iam.GetUserInput{})
	if err != nil {
		t.Fatalf("GetUser with no UserName: %v", err)
	}

	if aws.ToString(got.User.UserName) == "" {
		t.Fatal("caller UserName is empty")
	}

	if aws.ToString(got.User.Arn) == "" {
		t.Fatal("caller Arn is empty")
	}
}

// TestSDKListUsersPagination proves MaxItems/Marker are honored and IsTruncated
// reflects whether more pages remain.
func TestSDKListUsersPagination(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	for _, name := range []string{"u1", "u2", "u3", "u4", "u5"} {
		if _, err := client.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String(name)}); err != nil {
			t.Fatalf("CreateUser %s: %v", name, err)
		}
	}

	first, err := client.ListUsers(ctx, &iam.ListUsersInput{MaxItems: aws.Int32(2)})
	if err != nil {
		t.Fatalf("ListUsers page 1: %v", err)
	}

	if len(first.Users) != 2 {
		t.Fatalf("page 1: got %d users, want 2", len(first.Users))
	}

	if !first.IsTruncated {
		t.Fatal("page 1: IsTruncated = false, want true")
	}

	if aws.ToString(first.Marker) == "" {
		t.Fatal("page 1: Marker is empty despite truncation")
	}

	seen := len(first.Users)
	marker := first.Marker

	for marker != nil && aws.ToString(marker) != "" {
		next, err := client.ListUsers(ctx, &iam.ListUsersInput{
			MaxItems: aws.Int32(2), Marker: marker,
		})
		if err != nil {
			t.Fatalf("ListUsers page: %v", err)
		}

		seen += len(next.Users)
		marker = next.Marker
	}

	if seen != 5 {
		t.Fatalf("paginated total = %d users, want 5", seen)
	}
}
