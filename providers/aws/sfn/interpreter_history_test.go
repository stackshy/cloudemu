package sfn_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/sfn"
	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

// asyncMock builds a Step Functions mock with AsyncSettle and a FakeClock so
// Wait/Retry observably hold an execution RUNNING under a deterministic clock.
func asyncMock(t *testing.T, fc *config.FakeClock) *sfn.Mock {
	t.Helper()

	return sfn.New(config.NewOptions(
		config.WithClock(fc), config.WithRegion("us-east-1"),
		config.WithAccountID("000000000000"), config.WithAsyncSettle(),
	))
}

// assertChain verifies the ID/PreviousEventID chain: IDs are 1..n monotonic and
// each event's PreviousEventID points at the prior event (0 for the first).
func assertChain(t *testing.T, events []driver.HistoryEvent) {
	t.Helper()

	for i, e := range events {
		wantID := int64(i + 1)
		if e.ID != wantID {
			t.Fatalf("event %d ID = %d, want %d", i, e.ID, wantID)
		}

		var wantPrev int64
		if i > 0 {
			wantPrev = events[i-1].ID
		}

		if e.PreviousEventID != wantPrev {
			t.Fatalf("event %d PreviousEventID = %d, want %d", i, e.PreviousEventID, wantPrev)
		}
	}
}

// TestInterpreterHistorySequence asserts GetExecutionHistory returns the real
// per-state event sequence with a valid PreviousEventID chain, forward and
// reversed.
func TestInterpreterHistorySequence(t *testing.T) {
	def := `{"StartAt":"P","States":{
		"P":{"Type":"Pass","Result":{"step":1},"ResultPath":"$.p","Next":"C"},
		"C":{"Type":"Choice","Choices":[{"Variable":"$.p.step","NumericEquals":1,"Next":"S"}],"Default":"S"},
		"S":{"Type":"Succeed"}}}`

	m := newMock(t)
	exec := runSync(t, m, "hist", def, `{}`)
	ctx := context.Background()

	events, err := m.GetExecutionHistory(ctx, exec.ARN, false)
	if err != nil {
		t.Fatalf("GetExecutionHistory: %v", err)
	}

	wantTypes := []string{
		"ExecutionStarted",
		"PassStateEntered", "PassStateExited",
		"ChoiceStateEntered", "ChoiceStateExited",
		"SucceedStateEntered", "SucceedStateExited",
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

	rev, err := m.GetExecutionHistory(ctx, exec.ARN, true)
	if err != nil {
		t.Fatalf("GetExecutionHistory reverse: %v", err)
	}

	if rev[0].Type != "ExecutionSucceeded" || rev[len(rev)-1].Type != "ExecutionStarted" {
		t.Fatalf("reversed history ends = %q..%q, want ExecutionSucceeded..ExecutionStarted",
			rev[0].Type, rev[len(rev)-1].Type)
	}
}

// TestInterpreterSnapshotRoundTripHistory proves the event history rides the
// persisted execution through a snapshot/restore round trip.
func TestInterpreterSnapshotRoundTripHistory(t *testing.T) {
	def := `{"StartAt":"P","States":{"P":{"Type":"Pass","Result":{"a":1},"End":true}}}`

	ctx := context.Background()
	src := newMock(t)
	exec := runSync(t, src, "snap", def, `{}`)

	before, err := src.GetExecutionHistory(ctx, exec.ARN, false)
	if err != nil {
		t.Fatalf("history before snapshot: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newMock(t)
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	after, err := dst.GetExecutionHistory(ctx, exec.ARN, false)
	if err != nil {
		t.Fatalf("history after restore: %v", err)
	}

	if len(before) != len(after) {
		t.Fatalf("history length changed across restore: %d -> %d", len(before), len(after))
	}

	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("event %d differs after restore: %+v vs %+v", i, before[i], after[i])
		}
	}
}

// TestInterpreterWaitFakeClock pins deterministic Wait timing under AsyncSettle:
// the execution is RUNNING with a truncated history before the FakeClock is
// advanced past the settle window, then SUCCEEDED with the full history after.
func TestInterpreterWaitFakeClock(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	m := asyncMock(t, fc)
	ctx := context.Background()

	def := `{"StartAt":"W","States":{"W":{"Type":"Wait","Seconds":2,"Next":"S"},"S":{"Type":"Succeed"}}}`
	arn := createDef(t, m, "wait", def)

	start, err := m.StartExecution(ctx, driver.StartExecutionInput{StateMachineArn: arn, Name: "w1", Input: `{}`})
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}

	if start.Status != driver.ExecStatusRunning {
		t.Fatalf("initial status = %q, want RUNNING", start.Status)
	}

	hist, _ := m.GetExecutionHistory(ctx, start.ARN, false)
	if historyHasType(hist, "ExecutionSucceeded") {
		t.Fatalf("terminal event visible while RUNNING: %+v", hist)
	}

	// Base execution settle (1s) + Wait (2s) = 3s window.
	fc.Advance(3 * time.Second)

	got, _ := m.DescribeExecution(ctx, start.ARN)
	if got.Status != driver.ExecStatusSucceeded {
		t.Fatalf("status after advance = %q, want SUCCEEDED", got.Status)
	}

	hist, _ = m.GetExecutionHistory(ctx, start.ARN, false)
	if !historyHasType(hist, "WaitStateExited") || !historyHasType(hist, "ExecutionSucceeded") {
		t.Fatalf("settled history missing wait/terminal events: %+v", hist)
	}
}

// TestInterpreterWaitInstantWithoutAsyncSettle asserts a Wait completes instantly
// (SUCCEEDED, full history) when AsyncSettle is off — the default.
func TestInterpreterWaitInstantWithoutAsyncSettle(t *testing.T) {
	def := `{"StartAt":"W","States":{"W":{"Type":"Wait","Seconds":30,"Next":"S"},"S":{"Type":"Succeed"}}}`

	exec := runSync(t, newMock(t), "wait-instant", def, `{}`)

	if exec.Status != driver.ExecStatusSucceeded {
		t.Fatalf("status = %q, want instant SUCCEEDED", exec.Status)
	}
}

// TestInterpreterStopSurfacesErrorCause asserts StopExecution during the RUNNING
// window aborts the execution and persists the caller's Error/Cause, and that the
// history ends with ExecutionAborted.
func TestInterpreterStopSurfacesErrorCause(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	m := asyncMock(t, fc)
	ctx := context.Background()

	def := `{"StartAt":"W","States":{"W":{"Type":"Wait","Seconds":10,"Next":"S"},"S":{"Type":"Succeed"}}}`
	arn := createDef(t, m, "stop", def)

	start, _ := m.StartExecution(ctx, driver.StartExecutionInput{StateMachineArn: arn, Name: "s1", Input: `{}`})

	if _, err := m.StopExecution(ctx, start.ARN, "Halt", "operator stopped"); err != nil {
		t.Fatalf("StopExecution: %v", err)
	}

	got, _ := m.DescribeExecution(ctx, start.ARN)
	if got.Status != driver.ExecStatusAborted || got.Error != "Halt" || got.Cause != "operator stopped" {
		t.Fatalf("aborted execution = %+v, want ABORTED/Halt/operator stopped", got)
	}

	hist, _ := m.GetExecutionHistory(ctx, start.ARN, false)
	if len(hist) == 0 || hist[len(hist)-1].Type != "ExecutionAborted" {
		t.Fatalf("history does not end with ExecutionAborted: %+v", hist)
	}
}

// TestInterpreterMaxStepsGuard asserts a Choice/Next cycle fails loudly with the
// non-States.* transition-limit code rather than spinning forever.
func TestInterpreterMaxStepsGuard(t *testing.T) {
	def := `{"StartAt":"A","States":{
		"A":{"Type":"Choice","Choices":[{"Variable":"$.x","NumericEquals":1,"Next":"A"}],"Default":"A"}}}`

	exec := runSync(t, newMock(t), "cycle", def, `{"x":1}`)

	if exec.Status != driver.ExecStatusFailed {
		t.Fatalf("status = %q, want FAILED", exec.Status)
	}

	if exec.Error != "CloudEmu.StateTransitionLimitExceeded" {
		t.Fatalf("error = %q, want CloudEmu.StateTransitionLimitExceeded (a non-States.* code)", exec.Error)
	}
}

// TestInterpreterStartExecutionForwardsContext exercises the ctx-threaded entry
// point: StartExecution now forwards its context into runExecution/the
// interpreter (the plumbing the PR2 recursion guard rides). A value-carrying
// context runs to a real interpreted result.
func TestInterpreterStartExecutionForwardsContext(t *testing.T) {
	type ctxKey struct{}

	def := `{"StartAt":"P","States":{"P":{"Type":"Pass","Result":{"ok":true},"End":true}}}`
	m := newMock(t)
	arn := createDef(t, m, "ctx", def)

	ctx := context.WithValue(context.Background(), ctxKey{}, "v")
	exec, err := m.StartExecution(ctx, driver.StartExecutionInput{StateMachineArn: arn, Name: "c1", Input: `{}`})
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}

	if exec.Status == "" {
		t.Fatalf("expected an interpreted status, got empty: %+v", exec)
	}
}

func historyHasType(events []driver.HistoryEvent, typ string) bool {
	for _, e := range events {
		if e.Type == typ {
			return true
		}
	}

	return false
}
