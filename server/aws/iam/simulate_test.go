package iam_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

func decisionFor(results []iamtypes.EvaluationResult, action string) iamtypes.PolicyEvaluationDecisionType {
	for i := range results {
		if aws.ToString(results[i].EvalActionName) == action {
			return results[i].EvalDecision
		}
	}

	return ""
}

func TestSDKSimulatePrincipalPolicy(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	role, err := client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String("sim-role"),
		AssumeRolePolicyDocument: aws.String(trustPolicy),
	})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	policy, err := client.CreatePolicy(ctx, &iam.CreatePolicyInput{
		PolicyName:     aws.String("sim-policy"),
		PolicyDocument: aws.String(samplePolicy), // allows s3:ListBucket on *
	})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	if _, err := client.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
		RoleName:  aws.String("sim-role"),
		PolicyArn: policy.Policy.Arn,
	}); err != nil {
		t.Fatalf("AttachRolePolicy: %v", err)
	}

	out, err := client.SimulatePrincipalPolicy(ctx, &iam.SimulatePrincipalPolicyInput{
		PolicySourceArn: role.Role.Arn,
		ActionNames:     []string{"s3:ListBucket", "s3:DeleteObject"},
		ResourceArns:    []string{"arn:aws:s3:::my-bucket"},
	})
	if err != nil {
		t.Fatalf("SimulatePrincipalPolicy: %v", err)
	}

	if len(out.EvaluationResults) != 2 {
		t.Fatalf("got %d evaluation results, want 2", len(out.EvaluationResults))
	}

	if got := decisionFor(out.EvaluationResults, "s3:ListBucket"); got != iamtypes.PolicyEvaluationDecisionTypeAllowed {
		t.Fatalf("s3:ListBucket decision = %q, want allowed", got)
	}

	if got := decisionFor(out.EvaluationResults, "s3:DeleteObject"); got != iamtypes.PolicyEvaluationDecisionTypeImplicitDeny {
		t.Fatalf("s3:DeleteObject decision = %q, want implicitDeny", got)
	}
}

func TestSDKSimulateCustomPolicy(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	out, err := client.SimulateCustomPolicy(ctx, &iam.SimulateCustomPolicyInput{
		PolicyInputList: []string{samplePolicy}, // allows s3:ListBucket on *
		ActionNames:     []string{"s3:ListBucket", "s3:GetObject"},
	})
	if err != nil {
		t.Fatalf("SimulateCustomPolicy: %v", err)
	}

	if got := decisionFor(out.EvaluationResults, "s3:ListBucket"); got != iamtypes.PolicyEvaluationDecisionTypeAllowed {
		t.Fatalf("s3:ListBucket decision = %q, want allowed", got)
	}

	if got := decisionFor(out.EvaluationResults, "s3:GetObject"); got != iamtypes.PolicyEvaluationDecisionTypeImplicitDeny {
		t.Fatalf("s3:GetObject decision = %q, want implicitDeny", got)
	}
}
