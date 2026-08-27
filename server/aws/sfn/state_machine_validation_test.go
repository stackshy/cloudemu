package sfn_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/smithy-go"
)

// errorCode returns the SFN service error code carried by err, or "" if err is
// not a modeled API error.
func errorCode(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode()
	}

	return ""
}

// TestSDKCreateStateMachineRequiresRoleArn covers F5: roleArn is required and
// validated as an IAM role ARN. An empty or malformed value is InvalidArn.
func TestSDKCreateStateMachineRequiresRoleArn(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)

	// Empty roleArn: the SDK's client-side required check is a nil-check, so an
	// explicit empty string reaches the server and must be rejected.
	_, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name: aws.String("empty-role"), Definition: aws.String(definition),
		RoleArn: aws.String(""),
	})
	if code := errorCode(err); code != "InvalidArn" {
		t.Fatalf("empty roleArn: want InvalidArn, got %q (err=%v)", code, err)
	}

	// Malformed roleArn: not an IAM role ARN.
	_, err = c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name: aws.String("bad-role"), Definition: aws.String(definition),
		RoleArn: aws.String("not-an-arn"),
	})
	if code := errorCode(err); code != "InvalidArn" {
		t.Fatalf("malformed roleArn: want InvalidArn, got %q (err=%v)", code, err)
	}

	// A valid IAM role ARN still creates the state machine.
	out, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name: aws.String("good-role"), Definition: aws.String(definition),
		RoleArn: aws.String("arn:aws:iam::123456789012:role/svc"),
	})
	if err != nil {
		t.Fatalf("valid roleArn should create, got %v", err)
	}
	if aws.ToString(out.StateMachineArn) == "" {
		t.Fatalf("expected a state machine ARN")
	}
}

// TestSDKUpdateStateMachineRequiresUpdatableField covers F3: UpdateStateMachine
// with neither definition nor roleArn is MissingRequiredParameter and must not
// bump the revision; a real update succeeds and changes the revision.
func TestSDKUpdateStateMachineRequiresUpdatableField(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)
	arn := createSM(t, c, "upd")

	before, err := c.DescribeStateMachine(ctx, &awssfn.DescribeStateMachineInput{
		StateMachineArn: aws.String(arn),
	})
	if err != nil {
		t.Fatalf("DescribeStateMachine: %v", err)
	}
	rev0 := aws.ToString(before.RevisionId)

	// Update with only the ARN (no updatable field) is rejected.
	_, err = c.UpdateStateMachine(ctx, &awssfn.UpdateStateMachineInput{
		StateMachineArn: aws.String(arn),
	})
	if code := errorCode(err); code != "MissingRequiredParameter" {
		t.Fatalf("empty update: want MissingRequiredParameter, got %q (err=%v)", code, err)
	}

	// The rejected update must not have bumped the revision.
	mid, err := c.DescribeStateMachine(ctx, &awssfn.DescribeStateMachineInput{
		StateMachineArn: aws.String(arn),
	})
	if err != nil {
		t.Fatalf("DescribeStateMachine: %v", err)
	}
	if got := aws.ToString(mid.RevisionId); got != rev0 {
		t.Fatalf("rejected update bumped revision: %q -> %q", rev0, got)
	}

	// A valid update (new definition) succeeds and changes the revision.
	newDef := `{"StartAt":"Done","States":{"Done":{"Type":"Succeed"}}}`
	upd, err := c.UpdateStateMachine(ctx, &awssfn.UpdateStateMachineInput{
		StateMachineArn: aws.String(arn), Definition: aws.String(newDef),
	})
	if err != nil {
		t.Fatalf("valid update should succeed, got %v", err)
	}
	if got := aws.ToString(upd.RevisionId); got == "" || got == rev0 {
		t.Fatalf("valid update should return a new revision, got %q (was %q)", got, rev0)
	}
}
