package iam_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

const boundaryArn = "arn:aws:iam::aws:policy/PowerUserAccess"

func TestSDKRolePermissionsBoundary(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String("bounded-role"),
		AssumeRolePolicyDocument: aws.String(trustPolicy),
		PermissionsBoundary:      aws.String(boundaryArn),
	})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	assertBoundary(t, "CreateRole", created.Role.PermissionsBoundary, boundaryArn)

	got, err := client.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String("bounded-role")})
	if err != nil {
		t.Fatalf("GetRole: %v", err)
	}

	assertBoundary(t, "GetRole", got.Role.PermissionsBoundary, boundaryArn)

	if _, err := client.DeleteRolePermissionsBoundary(ctx, &iam.DeleteRolePermissionsBoundaryInput{
		RoleName: aws.String("bounded-role"),
	}); err != nil {
		t.Fatalf("DeleteRolePermissionsBoundary: %v", err)
	}

	cleared, err := client.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String("bounded-role")})
	if err != nil {
		t.Fatalf("GetRole after delete: %v", err)
	}

	if cleared.Role.PermissionsBoundary != nil {
		t.Fatalf("expected no permissions boundary after delete, got %+v", cleared.Role.PermissionsBoundary)
	}

	if _, err := client.PutRolePermissionsBoundary(ctx, &iam.PutRolePermissionsBoundaryInput{
		RoleName:            aws.String("bounded-role"),
		PermissionsBoundary: aws.String(boundaryArn),
	}); err != nil {
		t.Fatalf("PutRolePermissionsBoundary: %v", err)
	}

	readded, err := client.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String("bounded-role")})
	if err != nil {
		t.Fatalf("GetRole after put: %v", err)
	}

	assertBoundary(t, "GetRole after put", readded.Role.PermissionsBoundary, boundaryArn)
}

func TestSDKUserPermissionsBoundary(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String("bounded-user")}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := client.PutUserPermissionsBoundary(ctx, &iam.PutUserPermissionsBoundaryInput{
		UserName:            aws.String("bounded-user"),
		PermissionsBoundary: aws.String(boundaryArn),
	}); err != nil {
		t.Fatalf("PutUserPermissionsBoundary: %v", err)
	}

	got, err := client.GetUser(ctx, &iam.GetUserInput{UserName: aws.String("bounded-user")})
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}

	assertBoundary(t, "GetUser", got.User.PermissionsBoundary, boundaryArn)

	if _, err := client.DeleteUserPermissionsBoundary(ctx, &iam.DeleteUserPermissionsBoundaryInput{
		UserName: aws.String("bounded-user"),
	}); err != nil {
		t.Fatalf("DeleteUserPermissionsBoundary: %v", err)
	}

	cleared, err := client.GetUser(ctx, &iam.GetUserInput{UserName: aws.String("bounded-user")})
	if err != nil {
		t.Fatalf("GetUser after delete: %v", err)
	}

	if cleared.User.PermissionsBoundary != nil {
		t.Fatalf("expected no permissions boundary after delete, got %+v", cleared.User.PermissionsBoundary)
	}
}

// TestSDKCreateUserPermissionsBoundary asserts CreateUser applies a
// PermissionsBoundary supplied at creation time (matching CreateRole), and
// echoes it back on the CreateUser response.
func TestSDKCreateUserPermissionsBoundary(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateUser(ctx, &iam.CreateUserInput{
		UserName:            aws.String("boundary-at-create"),
		PermissionsBoundary: aws.String(boundaryArn),
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	assertBoundary(t, "CreateUser", created.User.PermissionsBoundary, boundaryArn)

	got, err := client.GetUser(ctx, &iam.GetUserInput{UserName: aws.String("boundary-at-create")})
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}

	assertBoundary(t, "GetUser", got.User.PermissionsBoundary, boundaryArn)
}

func assertBoundary(t *testing.T, where string, b *iamtypes.AttachedPermissionsBoundary, wantArn string) {
	t.Helper()

	if b == nil {
		t.Fatalf("%s: expected permissions boundary, got nil", where)
	}

	if aws.ToString(b.PermissionsBoundaryArn) != wantArn {
		t.Fatalf("%s: boundary arn = %q, want %q", where, aws.ToString(b.PermissionsBoundaryArn), wantArn)
	}

	if b.PermissionsBoundaryType != iamtypes.PermissionsBoundaryAttachmentTypePolicy {
		t.Fatalf("%s: boundary type = %q, want %q",
			where, b.PermissionsBoundaryType, iamtypes.PermissionsBoundaryAttachmentTypePolicy)
	}
}
