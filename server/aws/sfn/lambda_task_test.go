package sfn_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
	sfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	serverless "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// TestSDKTaskLambdaHistoryDetails runs a Task->Lambda state machine end to end
// through the wire server and asserts GetExecutionHistory surfaces the
// LambdaFunction* sub-events with populated detail structs.
func TestSDKTaskLambdaHistoryDetails(t *testing.T) {
	ctx := context.Background()

	cloud := cloudemu.NewAWS()

	if _, err := cloud.Lambda.CreateFunction(ctx, serverless.FunctionConfig{
		Name: "taskfn", Runtime: "go1.x", Handler: "main", Memory: 128, Timeout: 30,
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	cloud.Lambda.RegisterHandler("taskfn", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte(`{"handled":true}`), nil
	})

	srv := awsserver.New(awsserver.Drivers{SFN: cloud.SFN})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	c := awssfn.NewFromConfig(cfg, func(o *awssfn.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	def := `{"StartAt":"T","States":{"T":{"Type":"Task",` +
		`"Resource":"arn:aws:lambda:us-east-1:000000000000:function:taskfn","End":true}}}`

	sm, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name: aws.String("task-sm"), Definition: aws.String(def),
		RoleArn: aws.String("arn:aws:iam::123456789012:role/svc"),
	})
	if err != nil {
		t.Fatalf("CreateStateMachine: %v", err)
	}

	start, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: sm.StateMachineArn, Name: aws.String("run"), Input: aws.String(`{}`),
	})
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}

	hist, err := c.GetExecutionHistory(ctx, &awssfn.GetExecutionHistoryInput{ExecutionArn: start.ExecutionArn})
	if err != nil {
		t.Fatalf("GetExecutionHistory: %v", err)
	}

	var sawScheduled, sawSucceeded bool

	for _, e := range hist.Events {
		if e.Type == sfntypes.HistoryEventTypeLambdaFunctionScheduled {
			if e.LambdaFunctionScheduledEventDetails == nil ||
				aws.ToString(e.LambdaFunctionScheduledEventDetails.Resource) == "" {
				t.Fatalf("LambdaFunctionScheduled has nil/empty details: %+v", e)
			}

			sawScheduled = true
		}

		if e.Type == sfntypes.HistoryEventTypeLambdaFunctionSucceeded {
			if e.LambdaFunctionSucceededEventDetails == nil {
				t.Fatalf("LambdaFunctionSucceeded has nil details: %+v", e)
			}

			sawSucceeded = true
		}
	}

	if !sawScheduled || !sawSucceeded {
		t.Fatalf("missing Lambda sub-events (scheduled=%v succeeded=%v)", sawScheduled, sawSucceeded)
	}
}
