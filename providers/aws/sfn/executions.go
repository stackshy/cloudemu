package sfn

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

// historyEventCount is the number of synthesized events per successful
// execution (ExecutionStarted, PassStateEntered, PassStateExited,
// ExecutionSucceeded).
const historyEventCount = 4

func (m *Mock) getExec(arn string) (*execData, error) {
	if !validExecutionARN(arn) {
		return nil, invalidArn("%q is not a valid execution ARN", arn)
	}

	ed, ok := m.executions.Get(arn)
	if !ok {
		return nil, execNotFound(arn)
	}

	return ed, nil
}

// runExecution builds and stores a synchronously-completed execution. The
// emulator does not interpret the ASL definition: the execution starts RUNNING
// and immediately transitions to SUCCEEDED with output echoing the input.
func (m *Mock) runExecution(in driver.StartExecutionInput) (*driver.Execution, error) {
	sd, err := m.getSM(in.StateMachineArn)
	if err != nil {
		return nil, err
	}

	sd.mu.RLock()
	smName := sd.sm.Name
	sd.mu.RUnlock()

	name := in.Name
	if name == "" {
		name = idgen.GenerateID("exec-")
	}

	arn := m.execARN(smName, name)
	now := m.now()

	output := in.Input
	if output == "" {
		output = emptyJSON
	}

	exec := driver.Execution{
		ARN: arn, Name: name, StateMachineArn: in.StateMachineArn,
		Status: driver.ExecStatusSucceeded, Input: in.Input, Output: output,
		StartDate: now, StopDate: now,
	}

	if !m.executions.SetIfAbsent(arn, &execData{exec: exec}) {
		return nil, execAlreadyExists(name)
	}

	out := exec

	return &out, nil
}

func (m *Mock) StartExecution(_ context.Context, in driver.StartExecutionInput) (*driver.Execution, error) {
	return m.runExecution(in)
}

func (m *Mock) StartSyncExecution(_ context.Context, in driver.StartExecutionInput) (*driver.Execution, error) {
	return m.runExecution(in)
}

func (m *Mock) DescribeExecution(_ context.Context, arn string) (*driver.Execution, error) {
	ed, err := m.getExec(arn)
	if err != nil {
		return nil, err
	}

	ed.mu.RLock()
	defer ed.mu.RUnlock()

	out := ed.exec

	return &out, nil
}

func (m *Mock) StopExecution(_ context.Context, arn, errCode, cause string) (time.Time, error) {
	ed, err := m.getExec(arn)
	if err != nil {
		return time.Time{}, err
	}

	ed.mu.Lock()
	defer ed.mu.Unlock()

	now := m.now()

	// Only a still-running execution transitions to ABORTED; already-terminal
	// executions keep their terminal state but report a stop date.
	if ed.exec.Status == driver.ExecStatusRunning {
		ed.exec.Status = driver.ExecStatusAborted
		ed.exec.Error = errCode
		ed.exec.Cause = cause
		ed.exec.StopDate = now
	}

	return now, nil
}

func (m *Mock) ListExecutions(_ context.Context, stateMachineArn, statusFilter string) ([]driver.Execution, error) {
	if _, err := m.getSM(stateMachineArn); err != nil {
		return nil, err
	}

	all := m.executions.SortedValues()
	out := make([]driver.Execution, 0, len(all))

	for _, ed := range all {
		ed.mu.RLock()
		exec := ed.exec
		ed.mu.RUnlock()

		if exec.StateMachineArn != stateMachineArn {
			continue
		}

		if statusFilter != "" && exec.Status != statusFilter {
			continue
		}

		out = append(out, exec)
	}

	return out, nil
}

// GetExecutionHistory synthesizes a minimal but valid event list for a
// completed execution: ExecutionStarted -> PassStateEntered -> PassStateExited
// -> ExecutionSucceeded. No real ASL interpreter runs.
func (m *Mock) GetExecutionHistory(_ context.Context, arn string, reverse bool) ([]driver.HistoryEvent, error) {
	ed, err := m.getExec(arn)
	if err != nil {
		return nil, err
	}

	ed.mu.RLock()
	exec := ed.exec
	ed.mu.RUnlock()

	ts := exec.StartDate
	events := []driver.HistoryEvent{
		{ID: 1, PreviousEventID: 0, Type: "ExecutionStarted", Timestamp: ts, Input: exec.Input},
		{ID: 2, PreviousEventID: 1, Type: "PassStateEntered", Timestamp: ts},
		{ID: 3, PreviousEventID: 2, Type: "PassStateExited", Timestamp: ts},
		{ID: historyEventCount, PreviousEventID: 3, Type: "ExecutionSucceeded", Timestamp: exec.StopDate, Output: exec.Output},
	}

	if reverse {
		for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
			events[i], events[j] = events[j], events[i]
		}
	}

	return events, nil
}

// RedriveExecution restarts a previously-completed execution. The emulator does
// not re-run the workflow: it records a new redriveDate on the existing
// execution and returns it. Repeated calls advance the redrive date.
func (m *Mock) RedriveExecution(_ context.Context, arn string) (*driver.RedriveResult, error) {
	ed, err := m.getExec(arn)
	if err != nil {
		return nil, err
	}

	ed.mu.Lock()
	defer ed.mu.Unlock()

	now := m.now()
	ed.exec.Status = driver.ExecStatusSucceeded
	ed.exec.StopDate = now

	return &driver.RedriveResult{RedriveDate: now}, nil
}

func (m *Mock) DescribeStateMachineForExecution(_ context.Context, executionArn string) (*driver.StateMachine, error) {
	ed, err := m.getExec(executionArn)
	if err != nil {
		return nil, err
	}

	ed.mu.RLock()
	smArn := ed.exec.StateMachineArn
	ed.mu.RUnlock()

	return m.DescribeStateMachine(context.Background(), smArn)
}
