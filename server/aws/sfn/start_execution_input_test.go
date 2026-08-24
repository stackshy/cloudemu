package sfn_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/smithy-go"
)

// TestSDKStartExecutionInvalidInput covers StartExecution rejecting a non-JSON
// Input with InvalidExecutionInput (HTTP 400) instead of silently storing it and
// echoing it back as a SUCCEEDED output.
func TestSDKStartExecutionInvalidInput(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)
	arn := createSM(t, c, "input-guard")

	_, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: aws.String(arn),
		Input:           aws.String("not-json"),
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidExecutionInput" {
		t.Fatalf("StartExecution(invalid input) err = %v, want InvalidExecutionInput", err)
	}
}

// TestSDKStartExecutionValidInput confirms the happy path is intact: valid JSON
// input (and an omitted input) are accepted.
func TestSDKStartExecutionValidInput(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)
	arn := createSM(t, c, "input-ok")

	if _, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: aws.String(arn),
		Name:            aws.String("with-json"),
		Input:           aws.String(`{"k":"v"}`),
	}); err != nil {
		t.Fatalf("StartExecution(valid input): %v", err)
	}

	if _, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: aws.String(arn),
		Name:            aws.String("no-input"),
	}); err != nil {
		t.Fatalf("StartExecution(no input): %v", err)
	}
}
