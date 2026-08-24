package iam_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// seedAuthorizationAccount populates a small account: one user (in a group, with
// an attached managed policy), one role (with an inline policy, an attached
// managed policy, and an instance profile), one standalone group, and the
// managed policy carrying a second version. It returns the managed policy ARN.
func seedAuthorizationAccount(t *testing.T, client *iam.Client) string {
	t.Helper()
	ctx := context.Background()

	if _, err := client.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String("alice")}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := client.CreateGroup(ctx, &iam.CreateGroupInput{GroupName: aws.String("admins")}); err != nil {
		t.Fatalf("CreateGroup admins: %v", err)
	}

	if _, err := client.AddUserToGroup(ctx, &iam.AddUserToGroupInput{
		UserName: aws.String("alice"), GroupName: aws.String("admins"),
	}); err != nil {
		t.Fatalf("AddUserToGroup: %v", err)
	}

	if _, err := client.CreateGroup(ctx, &iam.CreateGroupInput{GroupName: aws.String("readers")}); err != nil {
		t.Fatalf("CreateGroup readers: %v", err)
	}

	policy, err := client.CreatePolicy(ctx, &iam.CreatePolicyInput{
		PolicyName:     aws.String("list-bucket"),
		PolicyDocument: aws.String(samplePolicy),
	})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	arn := aws.ToString(policy.Policy.Arn)

	if _, err := client.CreatePolicyVersion(ctx, &iam.CreatePolicyVersionInput{
		PolicyArn: aws.String(arn), PolicyDocument: aws.String(samplePolicy), SetAsDefault: true,
	}); err != nil {
		t.Fatalf("CreatePolicyVersion: %v", err)
	}

	if _, err := client.AttachUserPolicy(ctx, &iam.AttachUserPolicyInput{
		UserName: aws.String("alice"), PolicyArn: aws.String(arn),
	}); err != nil {
		t.Fatalf("AttachUserPolicy: %v", err)
	}

	seedAuthorizationRole(t, client, arn)

	return arn
}

func seedAuthorizationRole(t *testing.T, client *iam.Client, policyArn string) {
	t.Helper()
	ctx := context.Background()

	if _, err := client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String("app-role"),
		AssumeRolePolicyDocument: aws.String(trustPolicy),
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	if _, err := client.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
		RoleName:       aws.String("app-role"),
		PolicyName:     aws.String("inline-s3"),
		PolicyDocument: aws.String(samplePolicy),
	}); err != nil {
		t.Fatalf("PutRolePolicy: %v", err)
	}

	if _, err := client.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
		RoleName: aws.String("app-role"), PolicyArn: aws.String(policyArn),
	}); err != nil {
		t.Fatalf("AttachRolePolicy: %v", err)
	}

	if _, err := client.CreateInstanceProfile(ctx, &iam.CreateInstanceProfileInput{
		InstanceProfileName: aws.String("app-profile"),
	}); err != nil {
		t.Fatalf("CreateInstanceProfile: %v", err)
	}

	if _, err := client.AddRoleToInstanceProfile(ctx, &iam.AddRoleToInstanceProfileInput{
		InstanceProfileName: aws.String("app-profile"), RoleName: aws.String("app-role"),
	}); err != nil {
		t.Fatalf("AddRoleToInstanceProfile: %v", err)
	}
}

func TestSDKGetAccountAuthorizationDetails(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()
	arn := seedAuthorizationAccount(t, client)

	out, err := client.GetAccountAuthorizationDetails(ctx, &iam.GetAccountAuthorizationDetailsInput{})
	if err != nil {
		t.Fatalf("GetAccountAuthorizationDetails: %v", err)
	}

	if out.IsTruncated {
		t.Fatalf("IsTruncated = true, want false for a small account")
	}

	assertUserDetail(t, out.UserDetailList, arn)
	assertRoleDetail(t, out.RoleDetailList, arn)
	assertPolicyDetail(t, out.Policies, arn)

	if len(out.GroupDetailList) != 2 {
		t.Fatalf("got %d groups, want 2", len(out.GroupDetailList))
	}
}

func assertUserDetail(t *testing.T, users []iamtypes.UserDetail, policyArn string) {
	t.Helper()

	if len(users) != 1 {
		t.Fatalf("got %d users, want 1", len(users))
	}

	u := users[0]
	if aws.ToString(u.UserName) != "alice" {
		t.Fatalf("user name = %q, want alice", aws.ToString(u.UserName))
	}

	if len(u.GroupList) != 1 || u.GroupList[0] != "admins" {
		t.Fatalf("user GroupList = %v, want [admins]", u.GroupList)
	}

	if len(u.AttachedManagedPolicies) != 1 ||
		aws.ToString(u.AttachedManagedPolicies[0].PolicyArn) != policyArn {
		t.Fatalf("user AttachedManagedPolicies = %+v, want one entry for %s",
			u.AttachedManagedPolicies, policyArn)
	}
}

func assertRoleDetail(t *testing.T, roles []iamtypes.RoleDetail, policyArn string) {
	t.Helper()

	if len(roles) != 1 {
		t.Fatalf("got %d roles, want 1", len(roles))
	}

	r := roles[0]
	if aws.ToString(r.RoleName) != "app-role" {
		t.Fatalf("role name = %q, want app-role", aws.ToString(r.RoleName))
	}

	if len(r.RolePolicyList) != 1 || aws.ToString(r.RolePolicyList[0].PolicyName) != "inline-s3" {
		t.Fatalf("role RolePolicyList = %+v, want one inline-s3", r.RolePolicyList)
	}

	// The inline document must round-trip back to valid JSON via the SDK.
	var obj map[string]any
	if err := json.Unmarshal([]byte(aws.ToString(r.RolePolicyList[0].PolicyDocument)), &obj); err != nil {
		t.Fatalf("inline RolePolicy document not valid JSON: %v", err)
	}

	if len(r.AttachedManagedPolicies) != 1 ||
		aws.ToString(r.AttachedManagedPolicies[0].PolicyArn) != policyArn {
		t.Fatalf("role AttachedManagedPolicies = %+v, want one entry for %s",
			r.AttachedManagedPolicies, policyArn)
	}

	if len(r.InstanceProfileList) != 1 {
		t.Fatalf("got %d instance profiles on role, want 1", len(r.InstanceProfileList))
	}

	prof := r.InstanceProfileList[0]
	if len(prof.Roles) != 1 || aws.ToString(prof.Roles[0].RoleName) != "app-role" {
		t.Fatalf("instance profile Roles = %+v, want embedded app-role", prof.Roles)
	}
}

func assertPolicyDetail(t *testing.T, policies []iamtypes.ManagedPolicyDetail, policyArn string) {
	t.Helper()

	if len(policies) != 1 {
		t.Fatalf("got %d managed policies, want 1", len(policies))
	}

	p := policies[0]
	if aws.ToString(p.Arn) != policyArn {
		t.Fatalf("policy Arn = %q, want %q", aws.ToString(p.Arn), policyArn)
	}

	if aws.ToString(p.DefaultVersionId) != "v2" {
		t.Fatalf("policy DefaultVersionId = %q, want v2", aws.ToString(p.DefaultVersionId))
	}

	// Attached to alice + app-role.
	if aws.ToInt32(p.AttachmentCount) != 2 {
		t.Fatalf("policy AttachmentCount = %d, want 2", aws.ToInt32(p.AttachmentCount))
	}

	if len(p.PolicyVersionList) != 2 {
		t.Fatalf("got %d policy versions, want 2", len(p.PolicyVersionList))
	}
}

func TestSDKGetAccountAuthorizationDetailsRoleFilter(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()
	seedAuthorizationAccount(t, client)

	out, err := client.GetAccountAuthorizationDetails(ctx, &iam.GetAccountAuthorizationDetailsInput{
		Filter: []iamtypes.EntityType{iamtypes.EntityTypeRole},
	})
	if err != nil {
		t.Fatalf("GetAccountAuthorizationDetails: %v", err)
	}

	if len(out.RoleDetailList) != 1 {
		t.Fatalf("got %d roles, want 1", len(out.RoleDetailList))
	}

	if len(out.UserDetailList) != 0 || len(out.GroupDetailList) != 0 || len(out.Policies) != 0 {
		t.Fatalf("Role filter leaked other entities: users=%d groups=%d policies=%d",
			len(out.UserDetailList), len(out.GroupDetailList), len(out.Policies))
	}
}

func TestSDKGetAccountAuthorizationDetailsLocalPolicyFilter(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()
	arn := seedAuthorizationAccount(t, client)

	out, err := client.GetAccountAuthorizationDetails(ctx, &iam.GetAccountAuthorizationDetailsInput{
		Filter: []iamtypes.EntityType{iamtypes.EntityTypeLocalManagedPolicy},
	})
	if err != nil {
		t.Fatalf("GetAccountAuthorizationDetails: %v", err)
	}

	if len(out.Policies) != 1 || aws.ToString(out.Policies[0].Arn) != arn {
		t.Fatalf("LocalManagedPolicy filter = %+v, want the one customer policy", out.Policies)
	}

	if len(out.UserDetailList) != 0 || len(out.RoleDetailList) != 0 || len(out.GroupDetailList) != 0 {
		t.Fatalf("policy filter leaked entities: users=%d roles=%d groups=%d",
			len(out.UserDetailList), len(out.RoleDetailList), len(out.GroupDetailList))
	}
}

func TestSDKGetAccountAuthorizationDetailsPagination(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()
	seedAuthorizationAccount(t, client)

	// 1 user + 2 groups + 1 role + 1 policy = 5 entities across all four lists.
	const wantTotal = 5

	seen := 0
	var marker *string

	for pages := 0; pages < wantTotal+1; pages++ {
		out, err := client.GetAccountAuthorizationDetails(ctx, &iam.GetAccountAuthorizationDetailsInput{
			MaxItems: aws.Int32(1),
			Marker:   marker,
		})
		if err != nil {
			t.Fatalf("GetAccountAuthorizationDetails page %d: %v", pages, err)
		}

		seen += len(out.UserDetailList) + len(out.GroupDetailList) +
			len(out.RoleDetailList) + len(out.Policies)

		if !out.IsTruncated {
			if aws.ToString(out.Marker) != "" {
				t.Fatalf("final page carried a Marker %q", aws.ToString(out.Marker))
			}

			marker = nil

			break
		}

		if aws.ToString(out.Marker) == "" {
			t.Fatalf("truncated page %d missing Marker", pages)
		}

		marker = out.Marker
	}

	if seen != wantTotal {
		t.Fatalf("paginated total = %d entities, want %d", seen, wantTotal)
	}
}

func TestSDKGetAccountAuthorizationDetailsEmpty(t *testing.T) {
	client := newSDKClient(t)

	out, err := client.GetAccountAuthorizationDetails(context.Background(),
		&iam.GetAccountAuthorizationDetailsInput{})
	if err != nil {
		t.Fatalf("GetAccountAuthorizationDetails: %v", err)
	}

	if out.IsTruncated || aws.ToString(out.Marker) != "" {
		t.Fatalf("empty account: IsTruncated=%v Marker=%q, want false/empty",
			out.IsTruncated, aws.ToString(out.Marker))
	}

	if len(out.UserDetailList) != 0 || len(out.GroupDetailList) != 0 ||
		len(out.RoleDetailList) != 0 || len(out.Policies) != 0 {
		t.Fatalf("empty account returned entities: %+v", out)
	}
}
