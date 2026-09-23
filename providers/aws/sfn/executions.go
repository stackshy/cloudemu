package sfn

import (
	"context"
	"encoding/json"
	"sort"
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

	// A run that is already closed when first observed (sync, or AsyncSettle
	// off) publishes its close side effects now; a settling run publishes them
	// at its first settled observation (settleClose) or when StopExecution
	// aborts it.
	closed := window.Settled(now)

	if !m.executions.SetIfAbsent(arn, &execData{exec: exec, settle: window, closeEmitted: closed}) {
		return m.idempotentReuse(arn, name, smType, in.Input, async, now)
	}

	m.emitStartedMetric(ctx, in.StateMachineArn, now)

	// Real Step Functions publishes status-change events for STANDARD
	// executions only (EXPRESS and StartSyncExecution runs emit none).
	if async && smType == driver.TypeStandard {
		m.emitExecutionStarted(ctx, &exec, closed)
	}

	if closed {
		m.executionClosed(ctx, &exec, "")
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

		return out
	}

	// A settled run stopped when its window elapsed, not when it was started.
	if !out.StopDate.IsZero() && out.StopDate.Before(w.ReadyAt) {
		out.StopDate = w.ReadyAt
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

// settleClose publishes an execution's close side effects the first time it is
// observed settled (its AsyncSettle window elapsed). The flag flips under ed.mu;
// the publish runs after the lock is released, with the datapoints stamped at
// the run's StopDate.
func (m *Mock) settleClose(ctx context.Context, ed *execData, now time.Time) {
	ed.mu.Lock()

	if ed.closeEmitted || !ed.settle.Settled(now) {
		ed.mu.Unlock()
		return
	}

	ed.closeEmitted = true
	closed := observedExec(&ed.exec, ed.settle, now)

	ed.mu.Unlock()

	m.executionClosed(ctx, &closed, "")

	// Only an asynchronous run settles, so this is the terminal status change
	// of a StartExecution run that was still RUNNING when it started.
	if m.isStandard(closed.StateMachineArn) {
		m.emitExecutionStatus(ctx, &closed, closed.Status)
	}
}

func (m *Mock) DescribeExecution(ctx context.Context, arn string) (*driver.Execution, error) {
	ed, err := m.getExec(arn)
	if err != nil {
		return nil, err
	}

	m.settleClose(ctx, ed, m.now())

	ed.mu.RLock()
	defer ed.mu.RUnlock()

	out := observedExec(&ed.exec, ed.settle, m.now())

	return &out, nil
}

func (m *Mock) StopExecution(ctx context.Context, arn, errCode, cause string) (time.Time, error) {
	ed, err := m.getExec(arn)
	if err != nil {
		return time.Time{}, err
	}

	// A run that already settled (but was not yet observed) closes first.
	m.settleClose(ctx, ed, m.now())

	stopDate, aborted := m.abortExecution(ed, errCode, cause)

	// Published after ed.mu is released: a rule target may call back into SFN.
	if aborted != nil {
		m.executionClosed(ctx, aborted, "")

		if m.isStandard(aborted.StateMachineArn) {
			m.emitExecutionStatus(ctx, aborted, driver.ExecStatusAborted)
		}
	}

	return stopDate, nil
}

// isStandard reports whether the state machine is a STANDARD workflow (the only
// type whose executions publish status-change events).
func (m *Mock) isStandard(smArn string) bool {
	sd, err := m.getSM(smArn)
	if err != nil {
		return false
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	return sd.sm.Type == driver.TypeStandard
}

// abortExecution is StopExecution's locked core. It returns the stop date and,
// when this call actually aborted a running execution, a copy of the aborted
// record (nil when the execution had already settled).
func (m *Mock) abortExecution(ed *execData, errCode, cause string) (time.Time, *driver.Execution) {
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
		ed.closeEmitted = true
		aborted := ed.exec

		return ed.exec.StopDate, &aborted
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

// ListExecutions returns the executions of stateMachineArn ordered the way
// real Step Functions does: most recently started first (ties broken by ARN
// for deterministic output when two executions share a start timestamp, e.g.
// under FakeClock).
func (m *Mock) ListExecutions(ctx context.Context, stateMachineArn, statusFilter string) ([]driver.Execution, error) {
	if _, err := m.getSM(stateMachineArn); err != nil {
		return nil, err
	}

	all := m.executions.SortedValues()
	out := make([]driver.Execution, 0, len(all))

	now := m.now()

	for _, ed := range all {
		m.settleClose(ctx, ed, now)

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

	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].StartDate.Equal(out[j].StartDate) {
			return out[i].StartDate.After(out[j].StartDate)
		}

		return out[i].ARN < out[j].ARN
	})

	return out, nil
}

// GetExecutionHistory returns the real per-state event list the interpreter
// produced. While an execution is still observably RUNNING (settle window
// unelapsed), the list is truncated to the events whose virtual Timestamp has
// elapsed, generalizing the previous "only ExecutionStarted while RUNNING"
// rule, so the terminal event is not yet visible. Reverse order is applied last.
func (m *Mock) GetExecutionHistory(ctx context.Context, arn string, reverse bool) ([]driver.HistoryEvent, error) {
	ed, err := m.getExec(arn)
	if err != nil {
		return nil, err
	}

	now := m.now()
	m.settleClose(ctx, ed, now)

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

// RedriveExecution restarts a FAILED, ABORTED or TIMED_OUT STANDARD execution,
// as real Step Functions does. The emulator does not re-run the workflow: the
// redriven run succeeds, is counted in redriveCount, stamps redriveDate, and
// publishes its RUNNING -> SUCCEEDED status changes. A RUNNING or SUCCEEDED
// execution, or any EXPRESS one, is rejected with ExecutionNotRedrivable.
func (m *Mock) RedriveExecution(ctx context.Context, arn string) (*driver.RedriveResult, error) {
	ed, err := m.getExec(arn)
	if err != nil {
		return nil, err
	}

	ed.mu.RLock()
	smArn := ed.exec.StateMachineArn
	ed.mu.RUnlock()

	// Real Step Functions redrives STANDARD workflows only.
	if !m.isStandard(smArn) {
		return nil, execNotRedrivable("Execution %s is not of type STANDARD and cannot be redriven", arn)
	}

	// A run that settled but was never observed publishes its own close first,
	// so the original failure is not lost behind the redrive.
	m.settleClose(ctx, ed, m.now())

	redriven, err := m.redrive(ed)
	if err != nil {
		return nil, err
	}

	m.executionClosed(ctx, &redriven, redrivenPrefix)
	m.emitExecutionStarted(ctx, &redriven, true)

	return &driver.RedriveResult{RedriveDate: redriven.RedriveDate}, nil
}

// redrive is RedriveExecution's locked core. It checks redrivability against
// the execution's observed status (a still-settling run is RUNNING) and returns
// the redriven record.
func (m *Mock) redrive(ed *execData) (driver.Execution, error) {
	ed.mu.Lock()
	defer ed.mu.Unlock()

	now := m.now()

	observed := observedExec(&ed.exec, ed.settle, now).Status
	if status, reason := driver.ExecutionRedriveStatus(observed); status != driver.RedriveStatusRedrivable {
		return driver.Execution{}, execNotRedrivable("%s", reason)
	}

	ed.exec.Status = driver.ExecStatusSucceeded
	ed.exec.StopDate = now
	ed.closeEmitted = true
	// The redriven run succeeded, so the prior failure's error/cause no longer
	// describe the execution.
	ed.exec.Error, ed.exec.Cause = "", ""
	ed.exec.RedriveCount++
	ed.exec.RedriveDate = now

	return ed.exec, nil
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
