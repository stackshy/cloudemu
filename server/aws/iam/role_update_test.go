package iam_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// TestSDKUpdateRole proves UpdateRole mutates Description and MaxSessionDuration
// in place (previously undispatched → InvalidAction).
func TestSDKUpdateRole(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String("editable"),
		AssumeRolePolicyDocument: aws.String(trustPolicy),
		Description:              aws.String("original"),
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	if _, err := client.UpdateRole(ctx, &iam.UpdateRoleInput{
		RoleName:           aws.String("editable"),
		Description:        aws.String("updated"),
		MaxSessionDuration: aws.Int32(7200),
	}); err != nil {
		t.Fatalf("UpdateRole: %v", err)
	}

	got, err := client.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String("editable")})
	if err != nil {
		t.Fatalf("GetRole: %v", err)
	}

	if desc := aws.ToString(got.Role.Description); desc != "updated" {
		t.Fatalf("Role.Description = %q, want %q", desc, "updated")
	}

	if d := aws.ToInt32(got.Role.MaxSessionDuration); d != 7200 {
		t.Fatalf("Role.MaxSessionDuration = %d, want 7200", d)
	}
}

// TestSDKUpdateAssumeRolePolicy proves the trust policy is replaced in place
// (previously undispatched → InvalidAction).
func TestSDKUpdateAssumeRolePolicy(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String("trust-role"),
		AssumeRolePolicyDocument: aws.String(trustPolicy),
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	const newTrust = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}`

	if _, err := client.UpdateAssumeRolePolicy(ctx, &iam.UpdateAssumeRolePolicyInput{
		RoleName:       aws.String("trust-role"),
		PolicyDocument: aws.String(newTrust),
	}); err != nil {
		t.Fatalf("UpdateAssumeRolePolicy: %v", err)
	}

	got, err := client.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String("trust-role")})
	if err != nil {
		t.Fatalf("GetRole: %v", err)
	}

	// The SDK URL-decodes AssumeRolePolicyDocument; assert the new principal made it through.
	if doc := aws.ToString(got.Role.AssumeRolePolicyDocument); !strings.Contains(doc, "lambda.amazonaws.com") {
		t.Fatalf("trust policy = %q, want it to mention lambda.amazonaws.com", doc)
	}
}

// TestSDKUpdateAssumeRolePolicyRejectsMalformed proves a non-JSON trust policy
// is rejected with MalformedPolicyDocumentException.
func TestSDKUpdateAssumeRolePolicyRejectsMalformed(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String("mal-role"),
		AssumeRolePolicyDocument: aws.String(trustPolicy),
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	_, err := client.UpdateAssumeRolePolicy(ctx, &iam.UpdateAssumeRolePolicyInput{
		RoleName:       aws.String("mal-role"),
		PolicyDocument: aws.String("not json"),
	})

	var malformed *iamtypes.MalformedPolicyDocumentException
	if !errors.As(err, &malformed) {
		t.Fatalf("UpdateAssumeRolePolicy with bad JSON: want MalformedPolicyDocumentException, got %v", err)
	}
}
