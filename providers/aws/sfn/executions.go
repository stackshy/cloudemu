package sfn

import (
	"context"
	"encoding/json"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

// historyEventCount is the number of synthesized events per successful
// execution (ExecutionStarted, PassStateEntered, PassStateExited,
// ExecutionSucceeded).
const historyEventCount = 4

// passStateName is the synthetic Pass state name the emulator reports in
// StateEntered/StateExited history events (no real ASL interpreter runs).
const passStateName = "PassState"

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
func (m *Mock) runExecution(in driver.StartExecutionInput, async bool) (*driver.Execution, error) {
	sd, err := m.getSM(in.StateMachineArn)
	if err != nil {
		return nil, err
	}

	// AWS rejects a non-JSON execution Input with InvalidExecutionInput. An empty
	// Input is allowed (it defaults to {}); any non-empty value must be valid JSON.
	if in.Input != "" && !json.Valid([]byte(in.Input)) {
		return nil, invalidExecutionInput("The provided JSON input data is not valid.")
	}

	sd.mu.RLock()
	smName := sd.sm.Name
	smType := sd.sm.Type
	sd.mu.RUnlock()

	name := in.Name
	if name == "" {
		name = idgen.GenerateID("exec-")
	}

	arn := m.execARN(arnRegion(in.StateMachineArn, m.opts.Region), smName, name)
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

	// A synchronous execution (StartSyncExecution) returns its terminal result
	// immediately, so it carries no settle window; only the asynchronous
	// StartExecution settles RUNNING -> SUCCEEDED.
	var window settle.Window
	if async {
		window = settle.Pending(driver.ExecStatusRunning, now,
			m.opts.SettleDuration(settle.DefaultExecutionSettle))
	}

	if !m.executions.SetIfAbsent(arn, &execData{exec: exec, settle: window}) {
		return m.idempotentReuse(arn, name, smType, in.Input, async, now)
	}

	out := observedExec(&exec, window, now)

	return &out, nil
}

// idempotentReuse resolves a StartExecution name collision. StartExecution is
// idempotent for STANDARD workflows: reusing the name of a still-running
// execution with the *same* input succeeds and returns that execution. A
// different input, a closed (settled) execution, or an EXPRESS workflow all
// yield ExecutionAlreadyExists.
func (m *Mock) idempotentReuse(arn, name, smType, input string, async bool, now time.Time) (*driver.Execution, error) {
	ed, ok := m.executions.Get(arn)
	if !ok || !async || smType != driver.TypeStandard {
		return nil, execAlreadyExists(name)
	}

	ed.mu.RLock()
	sameInput := ed.exec.Input == input
	running := !ed.settle.Settled(now)
	out := observedExec(&ed.exec, ed.settle, now)
	ed.mu.RUnlock()

	if sameInput && running {
		return &out, nil
	}

	return nil, execAlreadyExists(name)
}

// observedExec overlays a RUNNING settle window onto a stored (terminal)
// execution: while the window is unelapsed the execution reports RUNNING with no
// stop date and no output yet, exactly as a real in-flight execution does.
func observedExec(exec *driver.Execution, w settle.Window, now time.Time) driver.Execution {
	out := *exec
	if observed := w.Observe(now, out.Status); observed != out.Status {
		out.Status = observed
		out.StopDate = time.Time{}
		out.Output = ""
	}

	return out
}

func (m *Mock) StartExecution(_ context.Context, in driver.StartExecutionInput) (*driver.Execution, error) {
	return m.runExecution(in, true)
}

// StartExternal starts a state-machine execution on behalf of a cross-service
// event source (e.g. an EventBridge rule whose target is this state machine).
// It is the SFN counterpart to the SQS/SNS/Lambda external-delivery choke
// points: an unknown state machine is a no-op so a stale target never fails the
// caller. The event envelope is passed through as the execution input.
func (m *Mock) StartExternal(ctx context.Context, stateMachineARN, input string) error {
	if _, err := m.getSM(stateMachineARN); err != nil {
		return nil
	}

	_, err := m.StartExecution(ctx, driver.StartExecutionInput{
		StateMachineArn: stateMachineARN,
		Input:           input,
	})

	return err
}

func (m *Mock) StartSyncExecution(_ context.Context, in driver.StartExecutionInput) (*driver.Execution, error) {
	return m.runExecution(in, false)
}

func (m *Mock) DescribeExecution(_ context.Context, arn string) (*driver.Execution, error) {
	ed, err := m.getExec(arn)
	if err != nil {
		return nil, err
	}

	ed.mu.RLock()
	defer ed.mu.RUnlock()

	out := observedExec(&ed.exec, ed.settle, m.now())

	return &out, nil
}

func (m *Mock) StopExecution(_ context.Context, arn, _, _ string) (time.Time, error) {
	ed, err := m.getExec(arn)
	if err != nil {
		return time.Time{}, err
	}

	ed.mu.Lock()
	defer ed.mu.Unlock()

	now := m.now()

	// While an execution is still settling (observably RUNNING under AsyncSettle),
	// Stop aborts it: ABORTED with a stop date and no output, and the window is
	// cleared so it stays aborted. An already-settled (terminal) execution is not
	// re-stopped — StopExecution returns its recorded stop date.
	if !ed.settle.Settled(now) {
		ed.exec.Status = driver.ExecStatusAborted
		ed.exec.StopDate = now
		ed.exec.Output = ""
		ed.settle = settle.Window{}
	}

	return ed.exec.StopDate, nil
}

func (m *Mock) ListExecutions(_ context.Context, stateMachineArn, statusFilter string) ([]driver.Execution, error) {
	if _, err := m.getSM(stateMachineArn); err != nil {
		return nil, err
	}

	all := m.executions.SortedValues()
	out := make([]driver.Execution, 0, len(all))

	now := m.now()

	for _, ed := range all {
		ed.mu.RLock()
		exec := observedExec(&ed.exec, ed.settle, now)
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
	settled := ed.settle.Settled(m.now())
	ed.mu.RUnlock()

	ts := exec.StartDate
	// While an execution is still observably RUNNING, only the start event has
	// happened; the terminal ExecutionSucceeded is not yet in the history,
	// matching the RUNNING status the Describe surface reports.
	if !settled {
		events := []driver.HistoryEvent{
			{ID: 1, PreviousEventID: 0, Type: "ExecutionStarted", Timestamp: ts, Input: exec.Input},
		}

		return events, nil
	}

	events := []driver.HistoryEvent{
		{ID: 1, PreviousEventID: 0, Type: "ExecutionStarted", Timestamp: ts, Input: exec.Input},
		{ID: 2, PreviousEventID: 1, Type: "PassStateEntered", Timestamp: ts, StateName: passStateName, Input: exec.Input},
		{ID: 3, PreviousEventID: 2, Type: "PassStateExited", Timestamp: ts, StateName: passStateName, Output: exec.Output},
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
