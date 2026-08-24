package iam_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
)

// TestSDKListUsersPathPrefix verifies ListUsers honors PathPrefix, returning
// only users whose path begins with the requested prefix.
func TestSDKListUsersPathPrefix(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	mk := func(name, path string) {
		if _, err := client.CreateUser(ctx, &iam.CreateUserInput{
			UserName: aws.String(name), Path: aws.String(path),
		}); err != nil {
			t.Fatalf("CreateUser %s: %v", name, err)
		}
	}

	mk("alice", "/team-a/")
	mk("bob", "/team-a/")
	mk("carol", "/team-b/")

	out, err := client.ListUsers(ctx, &iam.ListUsersInput{PathPrefix: aws.String("/team-a/")})
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}

	if len(out.Users) != 2 {
		t.Fatalf("PathPrefix=/team-a/ returned %d users, want 2", len(out.Users))
	}

	for _, u := range out.Users {
		if !strings.HasPrefix(aws.ToString(u.Path), "/team-a/") {
			t.Fatalf("user %s path %q not under /team-a/", aws.ToString(u.UserName), aws.ToString(u.Path))
		}
	}

	// The default (no PathPrefix) still returns every user.
	all, err := client.ListUsers(ctx, &iam.ListUsersInput{})
	if err != nil {
		t.Fatalf("ListUsers all: %v", err)
	}

	if len(all.Users) != 3 {
		t.Fatalf("ListUsers with no PathPrefix returned %d users, want 3", len(all.Users))
	}
}

// TestSDKListRolesPathPrefix verifies ListRoles honors PathPrefix.
func TestSDKListRolesPathPrefix(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	mk := func(name, path string) {
		if _, err := client.CreateRole(ctx, &iam.CreateRoleInput{
			RoleName:                 aws.String(name),
			Path:                     aws.String(path),
			AssumeRolePolicyDocument: aws.String(trustPolicy),
		}); err != nil {
			t.Fatalf("CreateRole %s: %v", name, err)
		}
	}

	mk("svc-a", "/svc/")
	mk("svc-b", "/svc/")
	mk("app-x", "/app/")

	out, err := client.ListRoles(ctx, &iam.ListRolesInput{PathPrefix: aws.String("/svc/")})
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}

	if len(out.Roles) != 2 {
		t.Fatalf("PathPrefix=/svc/ returned %d roles, want 2", len(out.Roles))
	}
}

// TestSDKListGroupsPathPrefix verifies ListGroups honors PathPrefix.
func TestSDKListGroupsPathPrefix(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	mk := func(name, path string) {
		if _, err := client.CreateGroup(ctx, &iam.CreateGroupInput{
			GroupName: aws.String(name), Path: aws.String(path),
		}); err != nil {
			t.Fatalf("CreateGroup %s: %v", name, err)
		}
	}

	mk("eng-a", "/eng/")
	mk("eng-b", "/eng/")
	mk("ops-a", "/ops/")

	out, err := client.ListGroups(ctx, &iam.ListGroupsInput{PathPrefix: aws.String("/eng/")})
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}

	if len(out.Groups) != 2 {
		t.Fatalf("PathPrefix=/eng/ returned %d groups, want 2", len(out.Groups))
	}
}
