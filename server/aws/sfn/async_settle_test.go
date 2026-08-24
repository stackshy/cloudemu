package sfn_test

import (
	"context"
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

// TestAsyncSettleWireSFN pins that a real SDK client sees an execution as
// RUNNING (no stop date) until the settle window elapses, then SUCCEEDED, and
// that StopExecution during the RUNNING window aborts it — all over the wire.
func TestAsyncSettleWireSFN(t *testing.T) {
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

	start, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: sm.StateMachineArn, Name: aws.String("e1"), Input: aws.String("{}"),
	})
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}

	got, err := c.DescribeExecution(ctx, &awssfn.DescribeExecutionInput{ExecutionArn: start.ExecutionArn})
	if err != nil {
		t.Fatalf("DescribeExecution: %v", err)
	}
	if got.Status != sfntypes.ExecutionStatusRunning {
		t.Fatalf("status before settle = %q, want RUNNING", got.Status)
	}

	fc.Advance(2 * time.Second) // past DefaultExecutionSettle (1s)
	got, _ = c.DescribeExecution(ctx, &awssfn.DescribeExecutionInput{ExecutionArn: start.ExecutionArn})
	if got.Status != sfntypes.ExecutionStatusSucceeded {
		t.Fatalf("status after settle = %q, want SUCCEEDED", got.Status)
	}

	// Stop during the RUNNING window -> ABORTED.
	start2, _ := c.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: sm.StateMachineArn, Name: aws.String("e2"), Input: aws.String("{}"),
	})
	if _, err := c.StopExecution(ctx, &awssfn.StopExecutionInput{ExecutionArn: start2.ExecutionArn}); err != nil {
		t.Fatalf("StopExecution: %v", err)
	}
	got2, _ := c.DescribeExecution(ctx, &awssfn.DescribeExecutionInput{ExecutionArn: start2.ExecutionArn})
	if got2.Status != sfntypes.ExecutionStatusAborted {
		t.Fatalf("stopped status = %q, want ABORTED", got2.Status)
	}
}
