package sfn_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
	sfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"

	"github.com/stackshy/cloudemu/v2"
	cloudconfig "github.com/stackshy/cloudemu/v2/config"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestSDKStartExecutionIdempotent pins Route 53's STANDARD idempotency contract:
// reusing a running execution's name with the SAME input returns the SAME
// execution (200), a DIFFERENT input returns 400 ExecutionAlreadyExists, and
// once the execution has closed the name can no longer be reused.
func TestSDKStartExecutionIdempotent(t *testing.T) {
	fc := cloudconfig.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	cloud := cloudemu.NewAWS(cloudconfig.WithClock(fc), cloudconfig.WithAsyncSettle())
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{SFN: cloud.SFN}))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")))
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	c := awssfn.NewFromConfig(cfg, func(o *awssfn.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ctx := context.Background()

	sm, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name: aws.String("sm"), Definition: aws.String(definition),
		RoleArn: aws.String("arn:aws:iam::000000000000:role/r"),
	})
	if err != nil {
		t.Fatalf("CreateStateMachine: %v", err)
	}

	first, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: sm.StateMachineArn, Name: aws.String("dedup"), Input: aws.String(`{"a":1}`),
	})
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}

	// Same name + same input while the execution is still RUNNING: idempotent,
	// returns the identical executionArn and startDate.
	again, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: sm.StateMachineArn, Name: aws.String("dedup"), Input: aws.String(`{"a":1}`),
	})
	if err != nil {
		t.Fatalf("StartExecution(same name+input) should be idempotent, got: %v", err)
	}
	if aws.ToString(again.ExecutionArn) != aws.ToString(first.ExecutionArn) {
		t.Fatalf("idempotent executionArn = %q, want %q",
			aws.ToString(again.ExecutionArn), aws.ToString(first.ExecutionArn))
	}
	if !again.StartDate.Equal(*first.StartDate) {
		t.Fatalf("idempotent startDate = %v, want %v", again.StartDate, first.StartDate)
	}

	// Same name + DIFFERENT input while running: 400 ExecutionAlreadyExists.
	_, err = c.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: sm.StateMachineArn, Name: aws.String("dedup"), Input: aws.String(`{"a":2}`),
	})
	var already *sfntypes.ExecutionAlreadyExists
	if !errors.As(err, &already) {
		t.Fatalf("StartExecution(same name, diff input) = %v, want ExecutionAlreadyExists", err)
	}

	// Once the execution has closed (settle window elapsed), reusing the name —
	// even with the same input — is rejected.
	fc.Advance(2 * time.Second)

	_, err = c.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: sm.StateMachineArn, Name: aws.String("dedup"), Input: aws.String(`{"a":1}`),
	})
	if !errors.As(err, &already) {
		t.Fatalf("StartExecution(reuse closed name) = %v, want ExecutionAlreadyExists", err)
	}
}
