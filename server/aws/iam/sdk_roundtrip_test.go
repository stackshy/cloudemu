package iam_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsiam "github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/stackshy/cloudemu"
	awsserver "github.com/stackshy/cloudemu/server/aws"
)

const trustPolicy = `{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": {"Service": "ec2.amazonaws.com"},
    "Action": "sts:AssumeRole"
  }]
}`

const samplePolicy = `{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": ["s3:ListBucket"],
    "Resource": "*"
  }]
}`

func newSDKClient(t *testing.T) *awsiam.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		IAM: cloud.IAM,
		// EC2 is also wired so we exercise the dispatch precedence: the IAM
		// handler must claim the body before EC2 (the query-protocol catch-all)
		// sees it.
		EC2: cloud.EC2,
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsiam.NewFromConfig(cfg, func(o *awsiam.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKIAMUserLifecycle(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateUser(ctx, &awsiam.CreateUserInput{
		UserName: aws.String("alice"),
		Path:     aws.String("/team/"),
		Tags: []iamtypes.Tag{
			{Key: aws.String("env"), Value: aws.String("test")},
		},
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if aws.ToString(created.User.UserName) != "alice" {
		t.Fatalf("got user name %q, want alice", aws.ToString(created.User.UserName))
	}

	got, err := client.GetUser(ctx, &awsiam.GetUserInput{UserName: aws.String("alice")})
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}

	if aws.ToString(got.User.UserName) != "alice" {
		t.Fatalf("got user name %q after roundtrip, want alice", aws.ToString(got.User.UserName))
	}

	listed, err := client.ListUsers(ctx, &awsiam.ListUsersInput{})
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}

	if len(listed.Users) != 1 {
		t.Fatalf("got %d users, want 1", len(listed.Users))
	}

	if _, err := client.DeleteUser(ctx, &awsiam.DeleteUserInput{UserName: aws.String("alice")}); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	if _, err := client.GetUser(ctx, &awsiam.GetUserInput{UserName: aws.String("alice")}); err == nil {
		t.Fatalf("GetUser after delete: expected error, got nil")
	}
}

func TestSDKIAMRoleAndPolicy(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	role, err := client.CreateRole(ctx, &awsiam.CreateRoleInput{
		RoleName:                 aws.String("app-role"),
		AssumeRolePolicyDocument: aws.String(trustPolicy),
	})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	if aws.ToString(role.Role.RoleName) != "app-role" {
		t.Fatalf("got role %q, want app-role", aws.ToString(role.Role.RoleName))
	}

	policy, err := client.CreatePolicy(ctx, &awsiam.CreatePolicyInput{
		PolicyName:     aws.String("list-bucket"),
		PolicyDocument: aws.String(samplePolicy),
		Description:    aws.String("allow listing"),
	})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	policyArn := aws.ToString(policy.Policy.Arn)
	if policyArn == "" {
		t.Fatalf("CreatePolicy returned empty ARN")
	}

	if _, err := client.AttachRolePolicy(ctx, &awsiam.AttachRolePolicyInput{
		RoleName:  aws.String("app-role"),
		PolicyArn: aws.String(policyArn),
	}); err != nil {
		t.Fatalf("AttachRolePolicy: %v", err)
	}

	attached, err := client.ListAttachedRolePolicies(ctx, &awsiam.ListAttachedRolePoliciesInput{
		RoleName: aws.String("app-role"),
	})
	if err != nil {
		t.Fatalf("ListAttachedRolePolicies: %v", err)
	}

	if len(attached.AttachedPolicies) != 1 {
		t.Fatalf("got %d attached policies, want 1", len(attached.AttachedPolicies))
	}

	if aws.ToString(attached.AttachedPolicies[0].PolicyArn) != policyArn {
		t.Fatalf("got policy arn %q, want %q",
			aws.ToString(attached.AttachedPolicies[0].PolicyArn), policyArn)
	}

	if _, err := client.DetachRolePolicy(ctx, &awsiam.DetachRolePolicyInput{
		RoleName:  aws.String("app-role"),
		PolicyArn: aws.String(policyArn),
	}); err != nil {
		t.Fatalf("DetachRolePolicy: %v", err)
	}
}

func TestSDKIAMGroupsAndMembership(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateUser(ctx, &awsiam.CreateUserInput{
		UserName: aws.String("bob"),
	}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := client.CreateGroup(ctx, &awsiam.CreateGroupInput{
		GroupName: aws.String("admins"),
	}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	if _, err := client.AddUserToGroup(ctx, &awsiam.AddUserToGroupInput{
		UserName:  aws.String("bob"),
		GroupName: aws.String("admins"),
	}); err != nil {
		t.Fatalf("AddUserToGroup: %v", err)
	}

	groups, err := client.ListGroupsForUser(ctx, &awsiam.ListGroupsForUserInput{
		UserName: aws.String("bob"),
	})
	if err != nil {
		t.Fatalf("ListGroupsForUser: %v", err)
	}

	if len(groups.Groups) != 1 || aws.ToString(groups.Groups[0].GroupName) != "admins" {
		t.Fatalf("got groups %+v, want one entry named admins", groups.Groups)
	}

	if _, err := client.RemoveUserFromGroup(ctx, &awsiam.RemoveUserFromGroupInput{
		UserName:  aws.String("bob"),
		GroupName: aws.String("admins"),
	}); err != nil {
		t.Fatalf("RemoveUserFromGroup: %v", err)
	}
}

func TestSDKIAMAccessKeys(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateUser(ctx, &awsiam.CreateUserInput{
		UserName: aws.String("carol"),
	}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	created, err := client.CreateAccessKey(ctx, &awsiam.CreateAccessKeyInput{
		UserName: aws.String("carol"),
	})
	if err != nil {
		t.Fatalf("CreateAccessKey: %v", err)
	}

	keyID := aws.ToString(created.AccessKey.AccessKeyId)
	if keyID == "" {
		t.Fatalf("CreateAccessKey returned empty key id")
	}

	if aws.ToString(created.AccessKey.SecretAccessKey) == "" {
		t.Fatalf("CreateAccessKey returned empty secret")
	}

	listed, err := client.ListAccessKeys(ctx, &awsiam.ListAccessKeysInput{
		UserName: aws.String("carol"),
	})
	if err != nil {
		t.Fatalf("ListAccessKeys: %v", err)
	}

	if len(listed.AccessKeyMetadata) != 1 {
		t.Fatalf("got %d keys, want 1", len(listed.AccessKeyMetadata))
	}

	if _, err := client.DeleteAccessKey(ctx, &awsiam.DeleteAccessKeyInput{
		UserName:    aws.String("carol"),
		AccessKeyId: aws.String(keyID),
	}); err != nil {
		t.Fatalf("DeleteAccessKey: %v", err)
	}
}

func TestSDKIAMInstanceProfiles(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateRole(ctx, &awsiam.CreateRoleInput{
		RoleName:                 aws.String("ec2-role"),
		AssumeRolePolicyDocument: aws.String(trustPolicy),
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	profile, err := client.CreateInstanceProfile(ctx, &awsiam.CreateInstanceProfileInput{
		InstanceProfileName: aws.String("ec2-profile"),
	})
	if err != nil {
		t.Fatalf("CreateInstanceProfile: %v", err)
	}

	if aws.ToString(profile.InstanceProfile.InstanceProfileName) != "ec2-profile" {
		t.Fatalf("got profile name %q, want ec2-profile",
			aws.ToString(profile.InstanceProfile.InstanceProfileName))
	}

	if _, err := client.AddRoleToInstanceProfile(ctx, &awsiam.AddRoleToInstanceProfileInput{
		InstanceProfileName: aws.String("ec2-profile"),
		RoleName:            aws.String("ec2-role"),
	}); err != nil {
		t.Fatalf("AddRoleToInstanceProfile: %v", err)
	}

	got, err := client.GetInstanceProfile(ctx, &awsiam.GetInstanceProfileInput{
		InstanceProfileName: aws.String("ec2-profile"),
	})
	if err != nil {
		t.Fatalf("GetInstanceProfile: %v", err)
	}

	if len(got.InstanceProfile.Roles) != 1 ||
		aws.ToString(got.InstanceProfile.Roles[0].RoleName) != "ec2-role" {
		t.Fatalf("expected one role named ec2-role on profile, got %+v",
			got.InstanceProfile.Roles)
	}

	if _, err := client.RemoveRoleFromInstanceProfile(ctx, &awsiam.RemoveRoleFromInstanceProfileInput{
		InstanceProfileName: aws.String("ec2-profile"),
		RoleName:            aws.String("ec2-role"),
	}); err != nil {
		t.Fatalf("RemoveRoleFromInstanceProfile: %v", err)
	}

	if _, err := client.DeleteInstanceProfile(ctx, &awsiam.DeleteInstanceProfileInput{
		InstanceProfileName: aws.String("ec2-profile"),
	}); err != nil {
		t.Fatalf("DeleteInstanceProfile: %v", err)
	}
}
