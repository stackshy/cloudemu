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

	"github.com/stackshy/cloudemu/v2"
	cloudconfig "github.com/stackshy/cloudemu/v2/config"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestSDKListStateMachinesOrderedNewestFirst pins that ListStateMachines
// returns state machines most-recently-created first, matching real Step
// Functions — not alphabetical by name/ARN. The machine names are chosen so
// alphabetical and chronological order disagree.
func TestSDKListStateMachinesOrderedNewestFirst(t *testing.T) {
	fc := cloudconfig.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	cloud := cloudemu.NewAWS(cloudconfig.WithClock(fc))
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{SFN: cloud.SFN}))
	t.Cleanup(ts.Close)

	c := newOrderTestClient(t, ts.URL)
	ctx := context.Background()

	for _, n := range []string{"zzz-first", "aaa-second", "mmm-third"} {
		if _, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
			Name: aws.String(n), Definition: aws.String(definition),
			RoleArn: aws.String("arn:aws:iam::000000000000:role/r"),
		}); err != nil {
			t.Fatalf("CreateStateMachine(%s): %v", n, err)
		}
		fc.Advance(time.Second)
	}

	out, err := c.ListStateMachines(ctx, &awssfn.ListStateMachinesInput{})
	if err != nil {
		t.Fatalf("ListStateMachines: %v", err)
	}

	want := []string{"mmm-third", "aaa-second", "zzz-first"}
	if len(out.StateMachines) != len(want) {
		t.Fatalf("got %d state machines, want %d", len(out.StateMachines), len(want))
	}

	for i, w := range want {
		if got := aws.ToString(out.StateMachines[i].Name); got != w {
			t.Fatalf("position %d = %q, want %q", i, got, w)
		}
	}
}

// TestSDKListExecutionsOrderedNewestFirst pins that ListExecutions returns
// executions most-recently-started first, matching real Step Functions — not
// alphabetical by execution name/ARN.
func TestSDKListExecutionsOrderedNewestFirst(t *testing.T) {
	fc := cloudconfig.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	cloud := cloudemu.NewAWS(cloudconfig.WithClock(fc))
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{SFN: cloud.SFN}))
	t.Cleanup(ts.Close)

	c := newOrderTestClient(t, ts.URL)
	ctx := context.Background()

	sm, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name: aws.String("sm"), Definition: aws.String(definition),
		RoleArn: aws.String("arn:aws:iam::000000000000:role/r"),
	})
	if err != nil {
		t.Fatalf("CreateStateMachine: %v", err)
	}

	for _, n := range []string{"zzz-first", "aaa-second", "mmm-third"} {
		if _, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{
			StateMachineArn: sm.StateMachineArn, Name: aws.String(n), Input: aws.String("{}"),
		}); err != nil {
			t.Fatalf("StartExecution(%s): %v", n, err)
		}
		fc.Advance(time.Second)
	}

	out, err := c.ListExecutions(ctx, &awssfn.ListExecutionsInput{StateMachineArn: sm.StateMachineArn})
	if err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}

	want := []string{"mmm-third", "aaa-second", "zzz-first"}
	if len(out.Executions) != len(want) {
		t.Fatalf("got %d executions, want %d", len(out.Executions), len(want))
	}

	for i, w := range want {
		if got := aws.ToString(out.Executions[i].Name); got != w {
			t.Fatalf("position %d = %q, want %q", i, got, w)
		}
	}
}

func newOrderTestClient(t *testing.T, endpoint string) *awssfn.Client {
	t.Helper()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awssfn.NewFromConfig(cfg, func(o *awssfn.Options) { o.BaseEndpoint = aws.String(endpoint) })
}
