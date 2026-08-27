package iam_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
)

// TestSDKGetGroupReturnsMembers proves GetGroup lists the users added via
// AddUserToGroup (previously the membership was hardcoded empty).
func TestSDKGetGroupReturnsMembers(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateGroup(ctx, &iam.CreateGroupInput{GroupName: aws.String("devs")}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	for _, name := range []string{"ann", "bob"} {
		if _, err := client.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String(name)}); err != nil {
			t.Fatalf("CreateUser %s: %v", name, err)
		}

		if _, err := client.AddUserToGroup(ctx, &iam.AddUserToGroupInput{
			GroupName: aws.String("devs"), UserName: aws.String(name),
		}); err != nil {
			t.Fatalf("AddUserToGroup %s: %v", name, err)
		}
	}

	got, err := client.GetGroup(ctx, &iam.GetGroupInput{GroupName: aws.String("devs")})
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}

	if len(got.Users) != 2 {
		t.Fatalf("GetGroup Users = %d, want 2", len(got.Users))
	}

	names := map[string]bool{}
	for _, u := range got.Users {
		names[aws.ToString(u.UserName)] = true
	}

	if !names["ann"] || !names["bob"] {
		t.Fatalf("GetGroup Users = %v, want ann and bob", names)
	}
}

// TestSDKGroupIDIsPopulated proves group responses carry a non-empty GroupId
// (previously always empty).
func TestSDKGroupIDIsPopulated(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateGroup(ctx, &iam.CreateGroupInput{GroupName: aws.String("admins")})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	if id := aws.ToString(created.Group.GroupId); id == "" {
		t.Fatal("CreateGroup GroupId is empty")
	}

	got, err := client.GetGroup(ctx, &iam.GetGroupInput{GroupName: aws.String("admins")})
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}

	if id := aws.ToString(got.Group.GroupId); id == "" {
		t.Fatal("GetGroup GroupId is empty")
	}
}
