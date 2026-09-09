package iam_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// asMalformed reports whether err is IAM's MalformedPolicyDocumentException.
func asMalformed(err error) bool {
	var m *iamtypes.MalformedPolicyDocumentException

	return errors.As(err, &m)
}

// TestSDKCreatePolicyRejectsMissingStatement proves a managed-policy document
// that parses as JSON but omits the required Statement element is rejected with
// MalformedPolicyDocument, matching the IAM policy grammar (Statement is the one
// non-optional top-level block).
func TestSDKCreatePolicyRejectsMissingStatement(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, err := client.CreatePolicy(ctx, &iam.CreatePolicyInput{
		PolicyName:     aws.String("no-statement"),
		PolicyDocument: aws.String(`{"Version":"2012-10-17"}`),
	})
	if !asMalformed(err) {
		t.Fatalf("CreatePolicy without Statement: want MalformedPolicyDocumentException, got %v", err)
	}
}

// TestSDKCreateRoleRejectsMissingStatement proves a trust policy without a
// Statement element is rejected.
func TestSDKCreateRoleRejectsMissingStatement(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, err := client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String("no-stmt-trust"),
		AssumeRolePolicyDocument: aws.String(`{"Version":"2012-10-17"}`),
	})
	if !asMalformed(err) {
		t.Fatalf("CreateRole with statement-less trust policy: want MalformedPolicyDocumentException, got %v", err)
	}
}

// TestSDKPutRolePolicyRejectsMalformed proves an inline role policy that is not
// valid JSON is rejected (previously accepted unvalidated).
func TestSDKPutRolePolicyRejectsMalformed(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String("inline-role"),
		AssumeRolePolicyDocument: aws.String(trustPolicy),
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	_, err := client.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
		RoleName:       aws.String("inline-role"),
		PolicyName:     aws.String("bad"),
		PolicyDocument: aws.String("not-json"),
	})
	if !asMalformed(err) {
		t.Fatalf("PutRolePolicy with bad JSON: want MalformedPolicyDocumentException, got %v", err)
	}
}

// TestSDKPutUserPolicyRejectsMalformed proves an inline user policy that omits
// the Statement element is rejected.
func TestSDKPutUserPolicyRejectsMalformed(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateUser(ctx, &iam.CreateUserInput{
		UserName: aws.String("inline-user"),
	}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	_, err := client.PutUserPolicy(ctx, &iam.PutUserPolicyInput{
		UserName:       aws.String("inline-user"),
		PolicyName:     aws.String("bad"),
		PolicyDocument: aws.String(`{"Version":"2012-10-17"}`),
	})
	if !asMalformed(err) {
		t.Fatalf("PutUserPolicy without Statement: want MalformedPolicyDocumentException, got %v", err)
	}
}

// TestSDKCreatePolicyVersionRejectsMalformed proves a new policy version whose
// document is not valid JSON is rejected (previously accepted unvalidated).
func TestSDKCreatePolicyVersionRejectsMalformed(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreatePolicy(ctx, &iam.CreatePolicyInput{
		PolicyName:     aws.String("versioned"),
		PolicyDocument: aws.String(samplePolicy),
	})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	_, err = client.CreatePolicyVersion(ctx, &iam.CreatePolicyVersionInput{
		PolicyArn:      created.Policy.Arn,
		PolicyDocument: aws.String("not-json"),
	})
	if !asMalformed(err) {
		t.Fatalf("CreatePolicyVersion with bad JSON: want MalformedPolicyDocumentException, got %v", err)
	}
}

// TestSDKPolicyAcceptsSingleStatementObject proves the validator accepts a
// document whose Statement is a single object (not an array), which the IAM
// grammar permits.
func TestSDKPolicyAcceptsSingleStatementObject(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, err := client.CreatePolicy(ctx, &iam.CreatePolicyInput{
		PolicyName:     aws.String("single-stmt"),
		PolicyDocument: aws.String(`{"Version":"2012-10-17","Statement":{"Effect":"Allow","Action":"s3:*","Resource":"*"}}`),
	})
	if err != nil {
		t.Fatalf("CreatePolicy with single Statement object: unexpected error %v", err)
	}
}
