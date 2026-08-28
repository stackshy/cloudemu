package sfn

import (
	"context"
	"encoding/json"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/providers/aws/sfn/asl"
	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

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

// runExecution interprets the state machine's ASL definition, storing a
// synchronously-completed execution: it walks the graph from StartAt computing
// the terminal status/output (or Error/Cause on failure) and the full per-state
// history. ctx is threaded into the interpreter so a Task->Lambda seam (a later
// PR) carries recursionguard depth. The settle overlay keeps RUNNING observable
// under AsyncSettle; its window is extended by any Wait durations the run
// accumulated.
func (m *Mock) runExecution(ctx context.Context, in driver.StartExecutionInput, async bool) (*driver.Execution, error) {
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
	smName, smType, definition, roleArn := sd.sm.Name, sd.sm.Type, sd.sm.Definition, sd.sm.RoleArn
	sd.mu.RUnlock()

	name := in.Name
	if name == "" {
		name = idgen.GenerateID("exec-")
	}

	arn := m.execARN(arnRegion(in.StateMachineArn, m.opts.Region), smName, name)
	now := m.now()

	res := interpret(ctx, definition, &asl.RunInput{
		Input: in.Input, ExecArn: arn, ExecName: name, SMArn: in.StateMachineArn,
		SMName: smName, RoleArn: roleArn, StartTime: now, SettleBase: settle.DefaultExecutionSettle,
		InvokeLambda: m.lambdaInvoker(),
	})

	exec := execFromResult(arn, name, in, now, res)

	// A synchronous execution (StartSyncExecution) returns its terminal result
	// immediately, so it carries no settle window; only the asynchronous
	// StartExecution settles RUNNING -> terminal, over a window extended by Wait.
	var window settle.Window
	if async {
		window = settle.Pending(driver.ExecStatusRunning, now,
			m.opts.SettleDuration(settle.DefaultExecutionSettle+res.WaitTotal))
	}

	if !m.executions.SetIfAbsent(arn, &execData{exec: exec, settle: window}) {
		return m.idempotentReuse(arn, name, smType, in.Input, async, now)
	}

	out := observedExec(&exec, window, now)

	return &out, nil
}

// lambdaInvoker returns the Task->Lambda seam as the interpreter's callback, or
// nil when no Lambda backend is wired (library-only construction), in which case
// a Task echoes its input.
func (m *Mock) lambdaInvoker() asl.LambdaInvoker {
	if m.lambdaSync == nil {
		return nil
	}

	return m.lambdaSync.InvokeSync
}

// interpret parses and runs a definition. A definition accepted at create time
// always parses; a parse failure here (e.g. after an UpdateStateMachine that
// bypassed validation) fails the execution loudly rather than panicking.
func interpret(ctx context.Context, definition string, in *asl.RunInput) *asl.RunResult {
	def, err := asl.Parse(definition)
	if err != nil {
		return &asl.RunResult{
			Status: driver.ExecStatusFailed, Error: "States.Runtime", Cause: err.Error(),
			History: []driver.HistoryEvent{
				{ID: 1, Type: "ExecutionStarted", Timestamp: in.StartTime, Input: emptyOr(in.Input)},
				{ID: 2, PreviousEventID: 1, Type: "ExecutionFailed", Timestamp: in.StartTime,
					Error: "States.Runtime", Cause: err.Error()},
			},
		}
	}

	return asl.Run(ctx, def, in)
}

// execFromResult assembles the stored execution record from an interpreter run.
func execFromResult(
	arn, name string, in driver.StartExecutionInput, now time.Time, res *asl.RunResult,
) driver.Execution {
	return driver.Execution{
		ARN: arn, Name: name, StateMachineArn: in.StateMachineArn,
		Status: res.Status, Input: in.Input, Output: res.Output,
		Error: res.Error, Cause: res.Cause,
		StartDate: now, StopDate: now, History: res.History,
	}
}

// emptyOr returns "{}" for a blank execution input, else the input verbatim.
func emptyOr(input string) string {
	if input == "" {
		return emptyJSON
	}

	return input
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

func (m *Mock) StartExecution(ctx context.Context, in driver.StartExecutionInput) (*driver.Execution, error) {
	return m.runExecution(ctx, in, true)
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

func (m *Mock) StartSyncExecution(ctx context.Context, in driver.StartExecutionInput) (*driver.Execution, error) {
	return m.runExecution(ctx, in, false)
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

func (m *Mock) StopExecution(_ context.Context, arn, errCode, cause string) (time.Time, error) {
	ed, err := m.getExec(arn)
	if err != nil {
		return time.Time{}, err
	}

	ed.mu.Lock()
	defer ed.mu.Unlock()

	now := m.now()

	// While an execution is still settling (observably RUNNING under AsyncSettle),
	// Stop aborts it: ABORTED with a stop date, no output, the caller's error/cause
	// persisted, and the history trimmed to the events already visible plus a
	// terminal ExecutionAborted. The window is cleared so it stays aborted. An
	// already-settled (terminal) execution is not re-stopped.
	if !ed.settle.Settled(now) {
		ed.exec.Status = driver.ExecStatusAborted
		ed.exec.StopDate = now
		ed.exec.Output = ""
		ed.exec.Error = errCode
		ed.exec.Cause = cause
		ed.exec.History = abortHistory(ed.exec.History, now, errCode, cause)
		ed.settle = settle.Window{}
	}

	return ed.exec.StopDate, nil
}

// abortHistory trims an execution's history to the events already observable at
// now (those whose Timestamp has elapsed) and appends a terminal ExecutionAborted
// event, so an aborted run's history never shows its would-be terminal success.
func abortHistory(events []driver.HistoryEvent, now time.Time, errCode, cause string) []driver.HistoryEvent {
	visible := make([]driver.HistoryEvent, 0, len(events)+1)

	for i := range events {
		if !events[i].Timestamp.After(now) {
			visible = append(visible, events[i])
		}
	}

	var prev int64
	if n := len(visible); n > 0 {
		prev = visible[n-1].ID
	}

	return append(visible, driver.HistoryEvent{
		ID: prev + 1, PreviousEventID: prev, Type: "ExecutionAborted",
		Timestamp: now, Error: errCode, Cause: cause,
	})
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

// GetExecutionHistory returns the real per-state event list the interpreter
// produced. While an execution is still observably RUNNING (settle window
// unelapsed), the list is truncated to the events whose virtual Timestamp has
// elapsed — generalizing the previous "only ExecutionStarted while RUNNING"
// rule — so the terminal event is not yet visible. Reverse order is applied last.
func (m *Mock) GetExecutionHistory(_ context.Context, arn string, reverse bool) ([]driver.HistoryEvent, error) {
	ed, err := m.getExec(arn)
	if err != nil {
		return nil, err
	}

	now := m.now()

	ed.mu.RLock()
	settled := ed.settle.Settled(now)
	events := append([]driver.HistoryEvent(nil), ed.exec.History...)
	ed.mu.RUnlock()

	if !settled {
		visible := events[:0]

		for i := range events {
			if !events[i].Timestamp.After(now) {
				visible = append(visible, events[i])
			}
		}

		events = visible
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
