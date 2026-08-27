package iam_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
)

// TestSDKEntityARNEmbedsPath proves that a non-default Path is folded into the
// ARN of every path-bearing IAM entity, over the real wire protocol and the
// aws-sdk-go-v2 iam client. A path-less entity keeps the flat ARN (regression).
func TestSDKEntityARNEmbedsPath(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const path = "/div/sub/"

	user, err := client.CreateUser(ctx, &iam.CreateUserInput{
		UserName: aws.String("bob"), Path: aws.String(path),
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	assertARN(t, "user", user.User.Arn,
		"arn:aws:iam::123456789012:user/div/sub/bob")

	role, err := client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName: aws.String("r1"), Path: aws.String(path),
		AssumeRolePolicyDocument: aws.String(trustPolicy),
	})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	assertARN(t, "role", role.Role.Arn,
		"arn:aws:iam::123456789012:role/div/sub/r1")

	policy, err := client.CreatePolicy(ctx, &iam.CreatePolicyInput{
		PolicyName: aws.String("p1"), Path: aws.String(path),
		PolicyDocument: aws.String(samplePolicy),
	})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}
	assertARN(t, "policy", policy.Policy.Arn,
		"arn:aws:iam::123456789012:policy/div/sub/p1")

	group, err := client.CreateGroup(ctx, &iam.CreateGroupInput{
		GroupName: aws.String("g1"), Path: aws.String(path),
	})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	assertARN(t, "group", group.Group.Arn,
		"arn:aws:iam::123456789012:group/div/sub/g1")

	profile, err := client.CreateInstanceProfile(ctx, &iam.CreateInstanceProfileInput{
		InstanceProfileName: aws.String("ip1"), Path: aws.String(path),
	})
	if err != nil {
		t.Fatalf("CreateInstanceProfile: %v", err)
	}
	assertARN(t, "instance-profile", profile.InstanceProfile.Arn,
		"arn:aws:iam::123456789012:instance-profile/div/sub/ip1")
}

// TestSDKDefaultPathARNUnchanged is the regression guard: a create with no Path
// yields the flat ARN, exactly as before the path fix.
func TestSDKDefaultPathARNUnchanged(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	user, err := client.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String("flat")})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	assertARN(t, "user", user.User.Arn, "arn:aws:iam::123456789012:user/flat")

	role, err := client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName: aws.String("flatrole"), AssumeRolePolicyDocument: aws.String(trustPolicy),
	})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	assertARN(t, "role", role.Role.Arn, "arn:aws:iam::123456789012:role/flatrole")
}

func assertARN(t *testing.T, kind string, got *string, want string) {
	t.Helper()

	if actual := aws.ToString(got); actual != want {
		t.Fatalf("%s ARN = %q, want %q", kind, actual, want)
	}
}
