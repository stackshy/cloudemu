package iam_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// AWS-managed policies used to carry an empty placeholder document, so
// attaching one and then simulating an action against it always came back
// implicitDeny — the policy granted nothing. These tests attach a real
// AWS-managed policy through the SDK and simulate against it, the way a real
// caller checking "can my role do X" would, to confirm the curated documents
// actually grant what they say they grant.
func TestSDKManagedPolicyAdministratorAccessGrantsEverything(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	user, err := client.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String("admin-user")})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := client.AttachUserPolicy(ctx, &iam.AttachUserPolicyInput{
		UserName:  aws.String("admin-user"),
		PolicyArn: aws.String("arn:aws:iam::aws:policy/AdministratorAccess"),
	}); err != nil {
		t.Fatalf("AttachUserPolicy: %v", err)
	}

	out, err := client.SimulatePrincipalPolicy(ctx, &iam.SimulatePrincipalPolicyInput{
		PolicySourceArn: user.User.Arn,
		ActionNames:     []string{"dynamodb:DeleteTable", "ec2:TerminateInstances"},
	})
	if err != nil {
		t.Fatalf("SimulatePrincipalPolicy: %v", err)
	}

	for _, action := range []string{"dynamodb:DeleteTable", "ec2:TerminateInstances"} {
		if got := decisionFor(out.EvaluationResults, action); got != iamtypes.PolicyEvaluationDecisionTypeAllowed {
			t.Errorf("%s decision = %q, want allowed", action, got)
		}
	}
}

func TestSDKManagedPolicyS3ReadOnlyGrantsReadsNotWrites(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	user, err := client.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String("s3-reader")})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := client.AttachUserPolicy(ctx, &iam.AttachUserPolicyInput{
		UserName:  aws.String("s3-reader"),
		PolicyArn: aws.String("arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess"),
	}); err != nil {
		t.Fatalf("AttachUserPolicy: %v", err)
	}

	out, err := client.SimulatePrincipalPolicy(ctx, &iam.SimulatePrincipalPolicyInput{
		PolicySourceArn: user.User.Arn,
		ActionNames:     []string{"s3:GetObject", "s3:PutObject"},
		ResourceArns:    []string{"arn:aws:s3:::my-bucket/key"},
	})
	if err != nil {
		t.Fatalf("SimulatePrincipalPolicy: %v", err)
	}

	if got := decisionFor(out.EvaluationResults, "s3:GetObject"); got != iamtypes.PolicyEvaluationDecisionTypeAllowed {
		t.Errorf("s3:GetObject decision = %q, want allowed", got)
	}

	if got := decisionFor(out.EvaluationResults, "s3:PutObject"); got != iamtypes.PolicyEvaluationDecisionTypeImplicitDeny {
		t.Errorf("s3:PutObject decision = %q, want implicitDeny", got)
	}
}

func TestSDKManagedPolicyS3FullAccessGrantsWrites(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	user, err := client.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String("s3-writer")})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := client.AttachUserPolicy(ctx, &iam.AttachUserPolicyInput{
		UserName:  aws.String("s3-writer"),
		PolicyArn: aws.String("arn:aws:iam::aws:policy/AmazonS3FullAccess"),
	}); err != nil {
		t.Fatalf("AttachUserPolicy: %v", err)
	}

	out, err := client.SimulatePrincipalPolicy(ctx, &iam.SimulatePrincipalPolicyInput{
		PolicySourceArn: user.User.Arn,
		ActionNames:     []string{"s3:PutObject", "s3:DeleteObject"},
		ResourceArns:    []string{"arn:aws:s3:::my-bucket/key"},
	})
	if err != nil {
		t.Fatalf("SimulatePrincipalPolicy: %v", err)
	}

	for _, action := range []string{"s3:PutObject", "s3:DeleteObject"} {
		if got := decisionFor(out.EvaluationResults, action); got != iamtypes.PolicyEvaluationDecisionTypeAllowed {
			t.Errorf("%s decision = %q, want allowed", action, got)
		}
	}
}

// CheckPermission is the provider-level entry point the wire layer's own
// callers (e.g. instance-profile-linked EC2 calls) use directly, so it is
// worth confirming it reads the same filled-in documents SimulatePolicy does.
func TestSDKManagedPolicyDynamoDBFullAccessViaCheckPermission(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	role, err := client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String("dynamo-role"),
		AssumeRolePolicyDocument: aws.String(trustPolicy),
	})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	if _, err := client.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
		RoleName:  aws.String("dynamo-role"),
		PolicyArn: aws.String("arn:aws:iam::aws:policy/AmazonDynamoDBReadOnlyAccess"),
	}); err != nil {
		t.Fatalf("AttachRolePolicy: %v", err)
	}

	out, err := client.SimulatePrincipalPolicy(ctx, &iam.SimulatePrincipalPolicyInput{
		PolicySourceArn: role.Role.Arn,
		ActionNames:     []string{"dynamodb:Query", "dynamodb:DeleteTable"},
	})
	if err != nil {
		t.Fatalf("SimulatePrincipalPolicy: %v", err)
	}

	if got := decisionFor(out.EvaluationResults, "dynamodb:Query"); got != iamtypes.PolicyEvaluationDecisionTypeAllowed {
		t.Errorf("dynamodb:Query decision = %q, want allowed", got)
	}

	if got := decisionFor(out.EvaluationResults, "dynamodb:DeleteTable"); got != iamtypes.PolicyEvaluationDecisionTypeImplicitDeny {
		t.Errorf("dynamodb:DeleteTable decision = %q, want implicitDeny", got)
	}
}

// AmazonRDSFullAccess grants CloudWatch Logs read access as a companion to
// managing RDS instances, not unrestricted write/delete access to every log
// group in the account.
func TestSDKManagedPolicyRDSFullAccessDoesNotGrantUnrestrictedLogs(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	user, err := client.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String("rds-admin")})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := client.AttachUserPolicy(ctx, &iam.AttachUserPolicyInput{
		UserName:  aws.String("rds-admin"),
		PolicyArn: aws.String("arn:aws:iam::aws:policy/AmazonRDSFullAccess"),
	}); err != nil {
		t.Fatalf("AttachUserPolicy: %v", err)
	}

	out, err := client.SimulatePrincipalPolicy(ctx, &iam.SimulatePrincipalPolicyInput{
		PolicySourceArn: user.User.Arn,
		ActionNames:     []string{"rds:DeleteDBInstance", "logs:DescribeLogGroups", "logs:DeleteLogGroup", "logs:PutLogEvents"},
	})
	if err != nil {
		t.Fatalf("SimulatePrincipalPolicy: %v", err)
	}

	if got := decisionFor(out.EvaluationResults, "rds:DeleteDBInstance"); got != iamtypes.PolicyEvaluationDecisionTypeAllowed {
		t.Errorf("rds:DeleteDBInstance decision = %q, want allowed", got)
	}

	if got := decisionFor(out.EvaluationResults, "logs:DescribeLogGroups"); got != iamtypes.PolicyEvaluationDecisionTypeAllowed {
		t.Errorf("logs:DescribeLogGroups decision = %q, want allowed", got)
	}

	for _, action := range []string{"logs:DeleteLogGroup", "logs:PutLogEvents"} {
		if got := decisionFor(out.EvaluationResults, action); got != iamtypes.PolicyEvaluationDecisionTypeImplicitDeny {
			t.Errorf("%s decision = %q, want implicitDeny — RDSFullAccess must not grant unrestricted logs", action, got)
		}
	}
}

// CloudWatchFullAccess grants SNS only enough to manage alarm-notification
// topics, not full sns:* (Publish/DeleteTopic/AddPermission on every topic
// in the account).
func TestSDKManagedPolicyCloudWatchFullAccessDoesNotGrantUnrestrictedSNS(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	user, err := client.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String("cw-admin")})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := client.AttachUserPolicy(ctx, &iam.AttachUserPolicyInput{
		UserName:  aws.String("cw-admin"),
		PolicyArn: aws.String("arn:aws:iam::aws:policy/CloudWatchFullAccess"),
	}); err != nil {
		t.Fatalf("AttachUserPolicy: %v", err)
	}

	out, err := client.SimulatePrincipalPolicy(ctx, &iam.SimulatePrincipalPolicyInput{
		PolicySourceArn: user.User.Arn,
		ActionNames:     []string{"cloudwatch:PutMetricAlarm", "sns:CreateTopic", "sns:Publish", "sns:DeleteTopic"},
	})
	if err != nil {
		t.Fatalf("SimulatePrincipalPolicy: %v", err)
	}

	if got := decisionFor(out.EvaluationResults, "cloudwatch:PutMetricAlarm"); got != iamtypes.PolicyEvaluationDecisionTypeAllowed {
		t.Errorf("cloudwatch:PutMetricAlarm decision = %q, want allowed", got)
	}

	if got := decisionFor(out.EvaluationResults, "sns:CreateTopic"); got != iamtypes.PolicyEvaluationDecisionTypeAllowed {
		t.Errorf("sns:CreateTopic decision = %q, want allowed", got)
	}

	for _, action := range []string{"sns:Publish", "sns:DeleteTopic"} {
		if got := decisionFor(out.EvaluationResults, action); got != iamtypes.PolicyEvaluationDecisionTypeImplicitDeny {
			t.Errorf("%s decision = %q, want implicitDeny — CloudWatchFullAccess must not grant unrestricted sns", action, got)
		}
	}
}

// AmazonECS_FullAccess's real document grants a plain iam:PassRole so a
// caller can hand a task/execution role to the ECS service.
func TestSDKManagedPolicyECSFullAccessGrantsPassRole(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	user, err := client.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String("ecs-admin")})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := client.AttachUserPolicy(ctx, &iam.AttachUserPolicyInput{
		UserName:  aws.String("ecs-admin"),
		PolicyArn: aws.String("arn:aws:iam::aws:policy/AmazonECS_FullAccess"),
	}); err != nil {
		t.Fatalf("AttachUserPolicy: %v", err)
	}

	out, err := client.SimulatePrincipalPolicy(ctx, &iam.SimulatePrincipalPolicyInput{
		PolicySourceArn: user.User.Arn,
		ActionNames:     []string{"iam:PassRole"},
	})
	if err != nil {
		t.Fatalf("SimulatePrincipalPolicy: %v", err)
	}

	if got := decisionFor(out.EvaluationResults, "iam:PassRole"); got != iamtypes.PolicyEvaluationDecisionTypeAllowed {
		t.Errorf("iam:PassRole decision = %q, want allowed", got)
	}
}

// AmazonDynamoDBFullAccess grants only the real application-autoscaling
// scaling-management actions, not that service's full surface.
func TestSDKManagedPolicyDynamoDBFullAccessScopesApplicationAutoscaling(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	user, err := client.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String("dynamo-admin")})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := client.AttachUserPolicy(ctx, &iam.AttachUserPolicyInput{
		UserName:  aws.String("dynamo-admin"),
		PolicyArn: aws.String("arn:aws:iam::aws:policy/AmazonDynamoDBFullAccess"),
	}); err != nil {
		t.Fatalf("AttachUserPolicy: %v", err)
	}

	out, err := client.SimulatePrincipalPolicy(ctx, &iam.SimulatePrincipalPolicyInput{
		PolicySourceArn: user.User.Arn,
		ActionNames: []string{
			"dynamodb:DeleteTable",
			"application-autoscaling:RegisterScalableTarget",
			"application-autoscaling:DeleteScheduledAction",
		},
	})
	if err != nil {
		t.Fatalf("SimulatePrincipalPolicy: %v", err)
	}

	if got := decisionFor(out.EvaluationResults, "dynamodb:DeleteTable"); got != iamtypes.PolicyEvaluationDecisionTypeAllowed {
		t.Errorf("dynamodb:DeleteTable decision = %q, want allowed", got)
	}

	if got := decisionFor(out.EvaluationResults, "application-autoscaling:RegisterScalableTarget"); got != iamtypes.PolicyEvaluationDecisionTypeAllowed {
		t.Errorf("application-autoscaling:RegisterScalableTarget decision = %q, want allowed", got)
	}

	action := "application-autoscaling:DeleteScheduledAction"
	if got := decisionFor(out.EvaluationResults, action); got != iamtypes.PolicyEvaluationDecisionTypeImplicitDeny {
		t.Errorf("%s decision = %q, want implicitDeny — not in the real scaling-management action list", action, got)
	}
}
