package iam_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
)

// pageSize is the MaxItems used across the pagination tests: small enough that
// the fixtures span several pages.
const pageSize = 2

// TestSDKListInstanceProfilesPaginates proves ListInstanceProfiles honors
// MaxItems/Marker: the SDK paginator walks every profile exactly once and
// terminates (previously the handler returned all profiles with IsTruncated
// hardcoded false, so a paginator only ever saw one page).
func TestSDKListInstanceProfilesPaginates(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const total = 5

	want := map[string]bool{}

	for i := range total {
		name := fmt.Sprintf("profile-%02d", i)
		want[name] = true

		if _, err := client.CreateInstanceProfile(ctx, &iam.CreateInstanceProfileInput{
			InstanceProfileName: aws.String(name),
		}); err != nil {
			t.Fatalf("CreateInstanceProfile %s: %v", name, err)
		}
	}

	paginator := iam.NewListInstanceProfilesPaginator(client, &iam.ListInstanceProfilesInput{
		MaxItems: aws.Int32(pageSize),
	})

	got := map[string]bool{}
	pages := 0

	for paginator.HasMorePages() {
		out, err := paginator.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		pages++

		if len(out.InstanceProfiles) > pageSize {
			t.Fatalf("page returned %d profiles, want <= %d", len(out.InstanceProfiles), pageSize)
		}

		for _, p := range out.InstanceProfiles {
			name := aws.ToString(p.InstanceProfileName)
			if got[name] {
				t.Fatalf("profile %s returned twice", name)
			}

			got[name] = true
		}
	}

	if len(got) != total {
		t.Fatalf("walked %d profiles, want %d", len(got), total)
	}

	for name := range want {
		if !got[name] {
			t.Fatalf("profile %s never returned", name)
		}
	}

	// total=5, pageSize=2 -> pages of 2, 2, 1.
	if wantPages := 3; pages != wantPages {
		t.Fatalf("walked %d pages, want %d", pages, wantPages)
	}
}

// TestSDKGetGroupMembersPaginate proves GetGroup pages its member list via
// Marker/MaxItems: the SDK paginator walks every group member exactly once and
// terminates (previously the Users list was returned whole with IsTruncated
// hardcoded false).
func TestSDKGetGroupMembersPaginate(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateGroup(ctx, &iam.CreateGroupInput{GroupName: aws.String("devs")}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	const total = 5

	want := map[string]bool{}

	for i := range total {
		name := fmt.Sprintf("user-%02d", i)
		want[name] = true

		if _, err := client.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String(name)}); err != nil {
			t.Fatalf("CreateUser %s: %v", name, err)
		}

		if _, err := client.AddUserToGroup(ctx, &iam.AddUserToGroupInput{
			GroupName: aws.String("devs"), UserName: aws.String(name),
		}); err != nil {
			t.Fatalf("AddUserToGroup %s: %v", name, err)
		}
	}

	paginator := iam.NewGetGroupPaginator(client, &iam.GetGroupInput{
		GroupName: aws.String("devs"),
		MaxItems:  aws.Int32(pageSize),
	})

	got := map[string]bool{}
	pages := 0

	for paginator.HasMorePages() {
		out, err := paginator.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		pages++

		if len(out.Users) > pageSize {
			t.Fatalf("page returned %d users, want <= %d", len(out.Users), pageSize)
		}

		for _, u := range out.Users {
			name := aws.ToString(u.UserName)
			if got[name] {
				t.Fatalf("user %s returned twice", name)
			}

			got[name] = true
		}
	}

	if len(got) != total {
		t.Fatalf("walked %d members, want %d", len(got), total)
	}

	for name := range want {
		if !got[name] {
			t.Fatalf("member %s never returned", name)
		}
	}

	if wantPages := 3; pages != wantPages {
		t.Fatalf("walked %d pages, want %d", pages, wantPages)
	}
}

// TestSDKGetGroupSinglePageNoMarker proves a group whose members fit in one page
// reports no Marker (the paginator stops after one page).
func TestSDKGetGroupSinglePageNoMarker(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateGroup(ctx, &iam.CreateGroupInput{GroupName: aws.String("ops")}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	if _, err := client.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String("solo")}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := client.AddUserToGroup(ctx, &iam.AddUserToGroupInput{
		GroupName: aws.String("ops"), UserName: aws.String("solo"),
	}); err != nil {
		t.Fatalf("AddUserToGroup: %v", err)
	}

	out, err := client.GetGroup(ctx, &iam.GetGroupInput{
		GroupName: aws.String("ops"),
		MaxItems:  aws.Int32(pageSize),
	})
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}

	if out.IsTruncated {
		t.Fatal("IsTruncated = true, want false for a single-page group")
	}

	if aws.ToString(out.Marker) != "" {
		t.Fatalf("Marker = %q, want empty for a single-page group", aws.ToString(out.Marker))
	}
}
