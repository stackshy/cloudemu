package sfn_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
	sfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"
	"github.com/aws/smithy-go"
)

// syncOpts lets StartSyncExecution reach the local server (it uses the
// "sync-" endpoint host prefix).
func syncOpts(o *awssfn.Options) { o.APIOptions = append(o.APIOptions, disableHostPrefix) }

// TestSDKStartSyncExecutionStandardTypeNotSupported pins the wire error a real
// SDK sees for StartSyncExecution on a STANDARD state machine.
func TestSDKStartSyncExecutionStandardTypeNotSupported(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)
	arn := createSM(t, c, "std-sync")

	_, err := c.StartSyncExecution(ctx, &awssfn.StartSyncExecutionInput{StateMachineArn: aws.String(arn)}, syncOpts)

	var notSupported *sfntypes.StateMachineTypeNotSupported
	if !errors.As(err, &notSupported) {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("want StateMachineTypeNotSupported, got %q", apiErr.ErrorCode())
		}

		t.Fatalf("want StateMachineTypeNotSupported, got %v", err)
	}
}

// TestSDKStartSyncExecutionExpress runs an EXPRESS workflow over the wire.
func TestSDKStartSyncExecutionExpress(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)

	created, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name:       aws.String("exp-sync"),
		Definition: aws.String(definition),
		RoleArn:    aws.String("arn:aws:iam::123456789012:role/svc"),
		Type:       sfntypes.StateMachineTypeExpress,
	})
	if err != nil {
		t.Fatalf("CreateStateMachine: %v", err)
	}

	out, err := c.StartSyncExecution(ctx, &awssfn.StartSyncExecutionInput{
		StateMachineArn: created.StateMachineArn, Input: aws.String(`{"k":1}`),
	}, syncOpts)
	if err != nil {
		t.Fatalf("StartSyncExecution(EXPRESS): %v", err)
	}

	if out.Status != sfntypes.SyncExecutionStatusSucceeded || aws.ToString(out.Output) != `{"k":1}` {
		t.Fatalf("got %s %q, want SUCCEEDED {\"k\":1}", out.Status, aws.ToString(out.Output))
	}
}
