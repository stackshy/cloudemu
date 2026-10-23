package sfn_test

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

// TestStartSyncExecutionRejectsStandard pins that StartSyncExecution is only
// offered for EXPRESS workflows: a STANDARD state machine is rejected with
// StateMachineTypeNotSupported and no execution is recorded.
func TestStartSyncExecutionRejectsStandard(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	arn := createSM(t, m, "std")

	_, err := m.StartSyncExecution(ctx, driver.StartExecutionInput{StateMachineArn: arn, Name: "s1"})

	var apiErr *driver.APIError
	if !stderrors.As(err, &apiErr) || apiErr.Exception != driver.ExStateMachineTypeNotSupported {
		t.Fatalf("StartSyncExecution(STANDARD) = %v, want StateMachineTypeNotSupported", err)
	}

	execs, err := m.ListExecutions(ctx, arn, "")
	if err != nil || len(execs) != 0 {
		t.Fatalf("ListExecutions = %d, %v; want no execution recorded", len(execs), err)
	}
}

// TestStartSyncExecutionExpress runs an EXPRESS workflow to completion.
func TestStartSyncExecutionExpress(t *testing.T) {
	m := newMock(t)
	arn := createExpressDef(t, m, "exp", definition)

	exec, err := m.StartSyncExecution(context.Background(),
		driver.StartExecutionInput{StateMachineArn: arn, Name: "s1", Input: `{"a":1}`})
	if err != nil {
		t.Fatalf("StartSyncExecution(EXPRESS): %v", err)
	}

	if exec.Status != driver.ExecStatusSucceeded || exec.Output != `{"a":1}` {
		t.Fatalf("execution = %s %q, want SUCCEEDED {\"a\":1}", exec.Status, exec.Output)
	}
}

// TestStartSyncExecutionMissingStateMachine checks existence before type.
func TestStartSyncExecutionMissingStateMachine(t *testing.T) {
	m := newMock(t)

	_, err := m.StartSyncExecution(context.Background(), driver.StartExecutionInput{
		StateMachineArn: "arn:aws:states:us-east-1:000000000000:stateMachine:missing",
	})

	var apiErr *driver.APIError
	if !stderrors.As(err, &apiErr) || apiErr.Exception != driver.ExStateMachineDoesNotExist {
		t.Fatalf("StartSyncExecution(missing) = %v, want StateMachineDoesNotExist", err)
	}
}

// TestStopExecutionTerminalKeepsStopDate pins StopExecution on a closed
// execution: 200 with the original stop date and no state change. The API
// reference documents no error for this case, and the behavior is unverified
// against live AWS.
func TestStopExecutionTerminalKeepsStopDate(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	arn := createSM(t, m, "stop-closed")

	exec, err := m.StartExecution(ctx, driver.StartExecutionInput{StateMachineArn: arn, Name: "done"})
	if err != nil || exec.Status != driver.ExecStatusSucceeded {
		t.Fatalf("StartExecution = %v, %v; want SUCCEEDED", exec, err)
	}

	stopDate, err := m.StopExecution(ctx, exec.ARN, "Halt", "late")
	if err != nil {
		t.Fatalf("StopExecution(closed): %v", err)
	}

	if !stopDate.Equal(exec.StopDate) {
		t.Fatalf("stopDate = %v, want the original %v", stopDate, exec.StopDate)
	}

	got, err := m.DescribeExecution(ctx, exec.ARN)
	if err != nil {
		t.Fatalf("DescribeExecution: %v", err)
	}

	if got.Status != driver.ExecStatusSucceeded || got.Error != "" || got.Cause != "" {
		t.Fatalf("after stop = %s %q/%q, want unchanged SUCCEEDED", got.Status, got.Error, got.Cause)
	}
}
