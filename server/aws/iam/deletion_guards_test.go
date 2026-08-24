package iam_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// TestSDKDeleteRoleWithInstanceProfile locks in that DeleteRole fails with
// DeleteConflictException while the role is still associated with an instance
// profile, matching API_DeleteRole (the caller must
// RemoveRoleFromInstanceProfile first).
func TestSDKDeleteRoleWithInstanceProfile(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String("r1"),
		AssumeRolePolicyDocument: aws.String(trustPolicy),
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	if _, err := client.CreateInstanceProfile(ctx, &iam.CreateInstanceProfileInput{
		InstanceProfileName: aws.String("ip1"),
	}); err != nil {
		t.Fatalf("CreateInstanceProfile: %v", err)
	}

	if _, err := client.AddRoleToInstanceProfile(ctx, &iam.AddRoleToInstanceProfileInput{
		InstanceProfileName: aws.String("ip1"),
		RoleName:            aws.String("r1"),
	}); err != nil {
		t.Fatalf("AddRoleToInstanceProfile: %v", err)
	}

	_, err := client.DeleteRole(ctx, &iam.DeleteRoleInput{RoleName: aws.String("r1")})

	var conflict *iamtypes.DeleteConflictException
	if !errors.As(err, &conflict) {
		t.Fatalf("DeleteRole while in instance profile: want DeleteConflictException, got %v", err)
	}

	// After removing the association the role should delete cleanly.
	if _, err := client.RemoveRoleFromInstanceProfile(ctx, &iam.RemoveRoleFromInstanceProfileInput{
		InstanceProfileName: aws.String("ip1"),
		RoleName:            aws.String("r1"),
	}); err != nil {
		t.Fatalf("RemoveRoleFromInstanceProfile: %v", err)
	}

	if _, err := client.DeleteRole(ctx, &iam.DeleteRoleInput{RoleName: aws.String("r1")}); err != nil {
		t.Fatalf("DeleteRole after removal: %v", err)
	}
}

// TestSDKDeleteInstanceProfileWithRole locks in that DeleteInstanceProfile
// fails with DeleteConflictException while a role is still associated, matching
// API_DeleteInstanceProfile.
func TestSDKDeleteInstanceProfileWithRole(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String("r1"),
		AssumeRolePolicyDocument: aws.String(trustPolicy),
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	if _, err := client.CreateInstanceProfile(ctx, &iam.CreateInstanceProfileInput{
		InstanceProfileName: aws.String("ip1"),
	}); err != nil {
		t.Fatalf("CreateInstanceProfile: %v", err)
	}

	if _, err := client.AddRoleToInstanceProfile(ctx, &iam.AddRoleToInstanceProfileInput{
		InstanceProfileName: aws.String("ip1"),
		RoleName:            aws.String("r1"),
	}); err != nil {
		t.Fatalf("AddRoleToInstanceProfile: %v", err)
	}

	_, err := client.DeleteInstanceProfile(ctx, &iam.DeleteInstanceProfileInput{
		InstanceProfileName: aws.String("ip1"),
	})

	var conflict *iamtypes.DeleteConflictException
	if !errors.As(err, &conflict) {
		t.Fatalf("DeleteInstanceProfile while role attached: want DeleteConflictException, got %v", err)
	}

	// The profile must still exist after the refused delete.
	if _, err := client.GetInstanceProfile(ctx, &iam.GetInstanceProfileInput{
		InstanceProfileName: aws.String("ip1"),
	}); err != nil {
		t.Fatalf("GetInstanceProfile after refused delete: %v", err)
	}
}

// TestSDKDeleteGroupWithMember locks in that DeleteGroup fails with
// DeleteConflictException while the group still has member users, matching
// API_DeleteGroup.
func TestSDKDeleteGroupWithMember(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateGroup(ctx, &iam.CreateGroupInput{GroupName: aws.String("g1")}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	if _, err := client.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String("u1")}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := client.AddUserToGroup(ctx, &iam.AddUserToGroupInput{
		GroupName: aws.String("g1"), UserName: aws.String("u1"),
	}); err != nil {
		t.Fatalf("AddUserToGroup: %v", err)
	}

	_, err := client.DeleteGroup(ctx, &iam.DeleteGroupInput{GroupName: aws.String("g1")})

	var conflict *iamtypes.DeleteConflictException
	if !errors.As(err, &conflict) {
		t.Fatalf("DeleteGroup with member: want DeleteConflictException, got %v", err)
	}

	// Group must survive the refused delete.
	if _, err := client.GetGroup(ctx, &iam.GetGroupInput{GroupName: aws.String("g1")}); err != nil {
		t.Fatalf("GetGroup after refused delete: %v", err)
	}
}
