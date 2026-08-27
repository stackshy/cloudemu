package iam_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// TestSDKGetAccountAuthorizationDetailsInlineAndAttached verifies GAAD reports a
// user's inline policies (UserPolicyList), a group's inline policies
// (GroupPolicyList), and a group's attached managed policies
// (AttachedManagedPolicies) — the same surfaces ListUserPolicies,
// ListGroupPolicies, and ListAttachedGroupPolicies expose individually.
func TestSDKGetAccountAuthorizationDetailsInlineAndAttached(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String("bob")}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := client.PutUserPolicy(ctx, &iam.PutUserPolicyInput{
		UserName:       aws.String("bob"),
		PolicyName:     aws.String("deny-billing"),
		PolicyDocument: aws.String(samplePolicy),
	}); err != nil {
		t.Fatalf("PutUserPolicy: %v", err)
	}

	if _, err := client.CreateGroup(ctx, &iam.CreateGroupInput{GroupName: aws.String("finance")}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	if _, err := client.PutGroupPolicy(ctx, &iam.PutGroupPolicyInput{
		GroupName:      aws.String("finance"),
		PolicyName:     aws.String("policygen"),
		PolicyDocument: aws.String(samplePolicy),
	}); err != nil {
		t.Fatalf("PutGroupPolicy: %v", err)
	}

	pol, err := client.CreatePolicy(ctx, &iam.CreatePolicyInput{
		PolicyName:     aws.String("admin-access"),
		PolicyDocument: aws.String(samplePolicy),
	})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	arn := aws.ToString(pol.Policy.Arn)

	if _, err := client.AttachGroupPolicy(ctx, &iam.AttachGroupPolicyInput{
		GroupName: aws.String("finance"), PolicyArn: aws.String(arn),
	}); err != nil {
		t.Fatalf("AttachGroupPolicy: %v", err)
	}

	out, err := client.GetAccountAuthorizationDetails(ctx, &iam.GetAccountAuthorizationDetailsInput{})
	if err != nil {
		t.Fatalf("GetAccountAuthorizationDetails: %v", err)
	}

	user := findUserDetail(t, out.UserDetailList, "bob")
	if len(user.UserPolicyList) != 1 || aws.ToString(user.UserPolicyList[0].PolicyName) != "deny-billing" {
		t.Fatalf("bob UserPolicyList = %+v, want one deny-billing", user.UserPolicyList)
	}

	group := findGroupDetail(t, out.GroupDetailList, "finance")
	if len(group.GroupPolicyList) != 1 || aws.ToString(group.GroupPolicyList[0].PolicyName) != "policygen" {
		t.Fatalf("finance GroupPolicyList = %+v, want one policygen", group.GroupPolicyList)
	}

	if len(group.AttachedManagedPolicies) != 1 ||
		aws.ToString(group.AttachedManagedPolicies[0].PolicyArn) != arn {
		t.Fatalf("finance AttachedManagedPolicies = %+v, want one entry for %s",
			group.AttachedManagedPolicies, arn)
	}
}

func findUserDetail(t *testing.T, users []iamtypes.UserDetail, name string) iamtypes.UserDetail {
	t.Helper()

	for i := range users {
		if aws.ToString(users[i].UserName) == name {
			return users[i]
		}
	}

	t.Fatalf("user %q not present in GAAD UserDetailList", name)

	return iamtypes.UserDetail{}
}

func findGroupDetail(t *testing.T, groups []iamtypes.GroupDetail, name string) iamtypes.GroupDetail {
	t.Helper()

	for i := range groups {
		if aws.ToString(groups[i].GroupName) == name {
			return groups[i]
		}
	}

	t.Fatalf("group %q not present in GAAD GroupDetailList", name)

	return iamtypes.GroupDetail{}
}
