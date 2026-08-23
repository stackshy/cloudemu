package iam_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// TestSDKPolicyDatesArePopulated proves CreatePolicy/GetPolicy return non-nil
// CreateDate and UpdateDate (previously both nil).
func TestSDKPolicyDatesArePopulated(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreatePolicy(ctx, &iam.CreatePolicyInput{
		PolicyName:     aws.String("dated-pol"),
		PolicyDocument: aws.String(samplePolicy),
	})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	if created.Policy.CreateDate == nil {
		t.Fatal("CreatePolicy CreateDate is nil")
	}

	if created.Policy.UpdateDate == nil {
		t.Fatal("CreatePolicy UpdateDate is nil")
	}

	got, err := client.GetPolicy(ctx, &iam.GetPolicyInput{PolicyArn: created.Policy.Arn})
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}

	if got.Policy.CreateDate == nil {
		t.Fatal("GetPolicy CreateDate is nil")
	}
}

// TestSDKPolicyAttachmentCount proves GetPolicy reflects the live attachment
// count (previously hardcoded 0).
func TestSDKPolicyAttachmentCount(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreatePolicy(ctx, &iam.CreatePolicyInput{
		PolicyName:     aws.String("counted-pol"),
		PolicyDocument: aws.String(samplePolicy),
	})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	arn := created.Policy.Arn

	if _, err := client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String("attach-role"),
		AssumeRolePolicyDocument: aws.String(trustPolicy),
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	if _, err := client.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
		RoleName: aws.String("attach-role"), PolicyArn: arn,
	}); err != nil {
		t.Fatalf("AttachRolePolicy: %v", err)
	}

	got, err := client.GetPolicy(ctx, &iam.GetPolicyInput{PolicyArn: arn})
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}

	if c := aws.ToInt32(got.Policy.AttachmentCount); c != 1 {
		t.Fatalf("AttachmentCount = %d, want 1", c)
	}
}

// TestSDKListPoliciesOnlyAttached proves the OnlyAttached filter returns only
// policies with a live attachment (previously ignored).
func TestSDKListPoliciesOnlyAttached(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	attached, err := client.CreatePolicy(ctx, &iam.CreatePolicyInput{
		PolicyName:     aws.String("is-attached"),
		PolicyDocument: aws.String(samplePolicy),
	})
	if err != nil {
		t.Fatalf("CreatePolicy attached: %v", err)
	}

	if _, err := client.CreatePolicy(ctx, &iam.CreatePolicyInput{
		PolicyName:     aws.String("not-attached"),
		PolicyDocument: aws.String(samplePolicy),
	}); err != nil {
		t.Fatalf("CreatePolicy unattached: %v", err)
	}

	if _, err := client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String("filter-role"),
		AssumeRolePolicyDocument: aws.String(trustPolicy),
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	if _, err := client.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
		RoleName: aws.String("filter-role"), PolicyArn: attached.Policy.Arn,
	}); err != nil {
		t.Fatalf("AttachRolePolicy: %v", err)
	}

	listed, err := client.ListPolicies(ctx, &iam.ListPoliciesInput{
		OnlyAttached: true,
		Scope:        iamtypes.PolicyScopeTypeLocal,
	})
	if err != nil {
		t.Fatalf("ListPolicies: %v", err)
	}

	if len(listed.Policies) != 1 {
		t.Fatalf("OnlyAttached returned %d policies, want 1", len(listed.Policies))
	}

	if name := aws.ToString(listed.Policies[0].PolicyName); name != "is-attached" {
		t.Fatalf("OnlyAttached returned %q, want is-attached", name)
	}
}

// TestSDKCreatePolicyRejectsMalformed proves an unparseable PolicyDocument is
// rejected with MalformedPolicyDocumentException (previously accepted).
func TestSDKCreatePolicyRejectsMalformed(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, err := client.CreatePolicy(ctx, &iam.CreatePolicyInput{
		PolicyName:     aws.String("bad-pol"),
		PolicyDocument: aws.String("}{ not json"),
	})

	var malformed *iamtypes.MalformedPolicyDocumentException
	if !errors.As(err, &malformed) {
		t.Fatalf("CreatePolicy with bad JSON: want MalformedPolicyDocumentException, got %v", err)
	}
}
