package sfn_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	"github.com/stackshy/cloudemu/v2/providers/aws/sfn"
	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

const lambdaFuncARN = "arn:aws:lambda:us-east-1:000000000000:function:f"

// fakeLambda is a test LambdaSyncInvoker: its InvokeSync delegates to fn.
type fakeLambda struct {
	fn func(ctx context.Context, arn string, payload []byte) ([]byte, string, error)
}

func (f fakeLambda) InvokeSync(
	ctx context.Context, arn string, payload []byte,
) (output []byte, functionError string, err error) {
	return f.fn(ctx, arn, payload)
}

// taskMock builds a Step Functions mock with the given Task->Lambda seam wired.
func taskMock(t *testing.T, inv sfn.LambdaSyncInvoker) *sfn.Mock {
	t.Helper()

	m := newMock(t)
	m.SetLambdaSyncInvoker(inv)

	return m
}

// TestTaskInvokesLambdaReturnsOutput runs a bare-function-ARN Task and asserts
// the execution output is the Lambda's output threaded through the pipeline.
func TestTaskInvokesLambdaReturnsOutput(t *testing.T) {
	inv := fakeLambda{fn: func(_ context.Context, arn string, _ []byte) ([]byte, string, error) {
		if arn != lambdaFuncARN {
			t.Errorf("Task delivered to %q, want %q", arn, lambdaFuncARN)
		}

		return []byte(`{"result":42}`), "", nil
	}}

	def := `{"StartAt":"T","States":{"T":{"Type":"Task","Resource":"` + lambdaFuncARN + `","End":true}}}`

	exec := runSync(t, taskMock(t, inv), "task-out", def, `{"in":1}`)

	if exec.Status != driver.ExecStatusSucceeded {
		t.Fatalf("status = %q err=%q cause=%q", exec.Status, exec.Error, exec.Cause)
	}

	if !jsonEqual(t, exec.Output, `{"result":42}`) {
		t.Fatalf("output = %q, want the Lambda output", exec.Output)
	}
}

// TestTaskLambdaInvokeResultSelector uses the optimized lambda:invoke integration
// and a ResultSelector to reshape the response envelope, pinning both the
// Payload-wrapping and the ResultSelector stage.
func TestTaskLambdaInvokeResultSelector(t *testing.T) {
	inv := fakeLambda{fn: func(_ context.Context, _ string, payload []byte) ([]byte, string, error) {
		// The Payload template forwarded the whole input.
		if string(payload) != `{"v":7}` {
			t.Errorf("lambda payload = %q, want {\"v\":7}", payload)
		}

		return []byte(`{"doubled":14}`), "", nil
	}}

	def := `{"StartAt":"T","States":{"T":{"Type":"Task",
		"Resource":"arn:aws:states:::lambda:invoke",
		"Parameters":{"FunctionName":"f","Payload.$":"$"},
		"ResultSelector":{"body.$":"$.Payload"},"End":true}}}`

	exec := runSync(t, taskMock(t, inv), "task-rs", def, `{"v":7}`)

	if exec.Status != driver.ExecStatusSucceeded {
		t.Fatalf("status = %q err=%q", exec.Status, exec.Error)
	}

	if !jsonEqual(t, exec.Output, `{"body":{"doubled":14}}`) {
		t.Fatalf("output = %q, want ResultSelector-reshaped {\"body\":{\"doubled\":14}}", exec.Output)
	}
}

// TestTaskCatchRoutesError asserts a Lambda functionError becomes States.TaskFailed
// and a matching Catcher merges the {Error,Cause} output at its ResultPath and
// transitions to its Next.
func TestTaskCatchRoutesError(t *testing.T) {
	inv := fakeLambda{fn: func(_ context.Context, _ string, _ []byte) ([]byte, string, error) {
		return nil, "boom", nil
	}}

	def := `{"StartAt":"T","States":{
		"T":{"Type":"Task","Resource":"` + lambdaFuncARN + `",
			"Catch":[{"ErrorEquals":["States.TaskFailed"],"Next":"H","ResultPath":"$.error"}],"End":true},
		"H":{"Type":"Pass","End":true}}}`

	exec := runSync(t, taskMock(t, inv), "task-catch", def, `{"keep":1}`)

	if exec.Status != driver.ExecStatusSucceeded {
		t.Fatalf("status = %q err=%q, want SUCCEEDED via Catch", exec.Status, exec.Error)
	}

	want := `{"keep":1,"error":{"Error":"States.TaskFailed","Cause":"boom"}}`
	if !jsonEqual(t, exec.Output, want) {
		t.Fatalf("output = %q, want the error merged at $.error over the raw input", exec.Output)
	}
}

// TestTaskRetryThenSucceed asserts a Task retries a failing Lambda and succeeds
// once the function stops failing, deterministically under the FakeClock.
func TestTaskRetryThenSucceed(t *testing.T) {
	attempts := 0
	inv := fakeLambda{fn: func(_ context.Context, _ string, _ []byte) ([]byte, string, error) {
		attempts++
		if attempts < 3 {
			return nil, "transient", nil
		}

		return []byte(`{"ok":true}`), "", nil
	}}

	def := `{"StartAt":"T","States":{"T":{"Type":"Task","Resource":"` + lambdaFuncARN + `",
		"Retry":[{"ErrorEquals":["States.TaskFailed"],"IntervalSeconds":1,"MaxAttempts":3,"BackoffRate":2}],
		"End":true}}}`

	exec := runSync(t, taskMock(t, inv), "task-retry-ok", def, `{}`)

	if exec.Status != driver.ExecStatusSucceeded {
		t.Fatalf("status = %q err=%q, want SUCCEEDED after retries", exec.Status, exec.Error)
	}

	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3 (initial + 2 retries)", attempts)
	}

	if !jsonEqual(t, exec.Output, `{"ok":true}`) {
		t.Fatalf("output = %q, want the eventual success", exec.Output)
	}
}

// TestTaskRetryExhaustedFails asserts a Task that keeps failing propagates
// States.TaskFailed once the matched Retrier's MaxAttempts is exhausted.
func TestTaskRetryExhaustedFails(t *testing.T) {
	attempts := 0
	inv := fakeLambda{fn: func(_ context.Context, _ string, _ []byte) ([]byte, string, error) {
		attempts++
		return nil, "always", nil
	}}

	def := `{"StartAt":"T","States":{"T":{"Type":"Task","Resource":"` + lambdaFuncARN + `",
		"Retry":[{"ErrorEquals":["States.ALL"],"IntervalSeconds":1,"MaxAttempts":2}],"End":true}}}`

	exec := runSync(t, taskMock(t, inv), "task-retry-fail", def, `{}`)

	if exec.Status != driver.ExecStatusFailed || exec.Error != "States.TaskFailed" {
		t.Fatalf("terminal = %q/%q, want FAILED/States.TaskFailed", exec.Status, exec.Error)
	}

	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3 (initial + 2 retries)", attempts)
	}
}

// TestTaskRetryBackoffDeterministicUnderAsyncSettle proves Retry backoff folds
// into the settle window: the execution holds RUNNING until the FakeClock is
// advanced past the base settle plus the backoff, then reports SUCCEEDED.
func TestTaskRetryBackoffDeterministicUnderAsyncSettle(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	m := asyncMock(t, fc)
	calls := 0
	m.SetLambdaSyncInvoker(fakeLambda{fn: func(_ context.Context, _ string, _ []byte) ([]byte, string, error) {
		calls++
		if calls == 1 {
			return nil, "transient", nil // first attempt fails, forcing one backoff
		}

		return []byte(`{"ok":true}`), "", nil
	}})
	ctx := context.Background()

	def := `{"StartAt":"T","States":{"T":{"Type":"Task","Resource":"` + lambdaFuncARN + `",
		"Retry":[{"ErrorEquals":["States.TaskFailed"],"IntervalSeconds":4,"MaxAttempts":1}],"End":true}}}`
	arn := createDef(t, m, "task-async-retry", def)

	start, err := m.StartExecution(ctx, driver.StartExecutionInput{StateMachineArn: arn, Name: "r1", Input: `{}`})
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}

	if start.Status != driver.ExecStatusRunning {
		t.Fatalf("initial status = %q, want RUNNING while backing off", start.Status)
	}

	// Base settle (1s) + Retry backoff (4s) = 5s window.
	fc.Advance(5 * time.Second)

	got, _ := m.DescribeExecution(ctx, start.ARN)
	if got.Status != driver.ExecStatusSucceeded {
		t.Fatalf("status after advance = %q, want SUCCEEDED", got.Status)
	}

	if calls != 2 {
		t.Fatalf("lambda calls = %d, want 2 (one failure + one success)", calls)
	}
}

// TestTaskNoInvokerEchoesInput asserts a Task with no Lambda seam wired falls
// back to echoing its input, preserving the pre-interpreter behavior.
func TestTaskNoInvokerEchoesInput(t *testing.T) {
	def := `{"StartAt":"T","States":{"T":{"Type":"Task","Resource":"` + lambdaFuncARN + `","End":true}}}`

	// newMock wires no LambdaSyncInvoker.
	exec := runSync(t, newMock(t), "task-echo", def, `{"echo":"me"}`)

	if exec.Status != driver.ExecStatusSucceeded {
		t.Fatalf("status = %q err=%q, want SUCCEEDED (echo fallback)", exec.Status, exec.Error)
	}

	if !jsonEqual(t, exec.Output, `{"echo":"me"}`) {
		t.Fatalf("output = %q, want the input echoed", exec.Output)
	}
}

// TestTaskLambdaHistoryEvents asserts the LambdaFunction* sub-events appear in
// GetExecutionHistory in order, bracketed by TaskStateEntered/Exited.
func TestTaskLambdaHistoryEvents(t *testing.T) {
	inv := fakeLambda{fn: func(_ context.Context, _ string, _ []byte) ([]byte, string, error) {
		return []byte(`{"r":1}`), "", nil
	}}

	def := `{"StartAt":"T","States":{"T":{"Type":"Task","Resource":"` + lambdaFuncARN + `","End":true}}}`

	m := taskMock(t, inv)
	exec := runSync(t, m, "task-hist", def, `{}`)
	ctx := context.Background()

	events, err := m.GetExecutionHistory(ctx, exec.ARN, false)
	if err != nil {
		t.Fatalf("GetExecutionHistory: %v", err)
	}

	wantTypes := []string{
		"ExecutionStarted",
		"TaskStateEntered",
		"LambdaFunctionScheduled", "LambdaFunctionStarted", "LambdaFunctionSucceeded",
		"TaskStateExited",
		"ExecutionSucceeded",
	}

	if len(events) != len(wantTypes) {
		t.Fatalf("history has %d events, want %d: %+v", len(events), len(wantTypes), events)
	}

	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Fatalf("event %d type = %q, want %q", i, events[i].Type, want)
		}
	}

	assertChain(t, events)

	if events[2].Resource != lambdaFuncARN {
		t.Fatalf("LambdaFunctionScheduled Resource = %q, want %q", events[2].Resource, lambdaFuncARN)
	}
}

// TestTaskRecursionGuardTerminates is the load-bearing recursion test: a Lambda
// seam that re-enters the FULL cycle — StartExecution on a state machine whose
// Task invokes Lambda again — terminates at recursionguard.MaxDepth with a
// bounded States.TaskFailed, never a stack overflow. It calls the REAL
// StartExecution entry point so ctx-threading on the production path is proven,
// and asserts the ctx depth increments across the StartExecution boundary.
func TestTaskRecursionGuardTerminates(t *testing.T) {
	m := newMock(t)

	def := `{"StartAt":"T","States":{"T":{"Type":"Task","Resource":"` + lambdaFuncARN + `","End":true}}}`
	arn := createDef(t, m, "recurse", def)

	maxDepth := 0
	m.SetLambdaSyncInvoker(fakeLambda{fn: func(ctx context.Context, _ string, payload []byte) ([]byte, string, error) {
		// Replicate the lambda adapter's guard, then re-enter the whole cycle
		// through the real StartExecution entry point.
		d := recursionguard.Depth(ctx)
		if d > maxDepth {
			maxDepth = d
		}

		if d >= recursionguard.MaxDepth {
			return nil, "recursion limit reached", nil
		}

		sub, err := m.StartSyncExecution(recursionguard.WithDepth(ctx, d+1),
			driver.StartExecutionInput{StateMachineArn: arn, Name: fmt.Sprintf("r%d", d), Input: string(payload)})
		if err != nil {
			return nil, "", err
		}

		if sub.Status == driver.ExecStatusFailed {
			return nil, sub.Error, nil // propagate the bounded failure up the chain
		}

		return []byte(sub.Output), "", nil
	}})

	top, err := m.StartExecution(context.Background(),
		driver.StartExecutionInput{StateMachineArn: arn, Name: "top", Input: `{}`})
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}

	if top.Status != driver.ExecStatusFailed || top.Error != "States.TaskFailed" {
		t.Fatalf("top terminal = %q/%q, want FAILED/States.TaskFailed (bounded recursion)", top.Status, top.Error)
	}

	if maxDepth != recursionguard.MaxDepth {
		t.Fatalf("max ctx depth = %d, want %d (guard reached across the StartExecution boundary)",
			maxDepth, recursionguard.MaxDepth)
	}
}
