package sfn_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

// TestParallelBranchesArrayOutput runs a Parallel with two branches and asserts
// the state output is the JSON array of branch outputs in branch order.
func TestParallelBranchesArrayOutput(t *testing.T) {
	def := `{"StartAt":"P","States":{
		"P":{"Type":"Parallel","End":true,"Branches":[
			{"StartAt":"A","States":{"A":{"Type":"Pass","Result":{"branch":1},"End":true}}},
			{"StartAt":"B","States":{"B":{"Type":"Pass","Result":{"branch":2},"End":true}}}
		]}}}`

	exec := runSync(t, newMock(t), "par", def, `{}`)

	if exec.Status != driver.ExecStatusSucceeded {
		t.Fatalf("status = %q (err=%q cause=%q), want SUCCEEDED", exec.Status, exec.Error, exec.Cause)
	}

	if !jsonEqual(t, exec.Output, `[{"branch":1},{"branch":2}]`) {
		t.Fatalf("output = %q, want the ordered branch-output array", exec.Output)
	}
}

// TestParallelBranchFailureCaught asserts a branch failure fails the Parallel
// state and is routed to the Parallel's Catch (not straight to ExecutionFailed).
func TestParallelBranchFailureCaught(t *testing.T) {
	def := `{"StartAt":"P","States":{
		"P":{"Type":"Parallel","End":true,
			"Branches":[
				{"StartAt":"A","States":{"A":{"Type":"Pass","End":true}}},
				{"StartAt":"F","States":{"F":{"Type":"Fail","Error":"BranchBoom","Cause":"branch blew up"}}}
			],
			"Catch":[{"ErrorEquals":["BranchBoom"],"Next":"Rescue","ResultPath":"$.error"}]},
		"Rescue":{"Type":"Pass","End":true}}}`

	exec := runSync(t, newMock(t), "par-catch", def, `{"seed":7}`)

	if exec.Status != driver.ExecStatusSucceeded {
		t.Fatalf("status = %q (err=%q cause=%q), want SUCCEEDED via Catch", exec.Status, exec.Error, exec.Cause)
	}

	if !jsonEqual(t, exec.Output, `{"seed":7,"error":{"Error":"BranchBoom","Cause":"branch blew up"}}`) {
		t.Fatalf("output = %q, want the raw input with the caught error merged at $.error", exec.Output)
	}
}

// TestParallelResultPathMergesOntoRawInput asserts ResultSelector/ResultPath on a
// Parallel splice the branch-output array onto the RAW input, preserving siblings.
func TestParallelResultPathMergesOntoRawInput(t *testing.T) {
	def := `{"StartAt":"P","States":{
		"P":{"Type":"Parallel","End":true,"ResultPath":"$.results",
			"Branches":[
				{"StartAt":"A","States":{"A":{"Type":"Pass","Result":1,"End":true}}},
				{"StartAt":"B","States":{"B":{"Type":"Pass","Result":2,"End":true}}}
			]}}}`

	exec := runSync(t, newMock(t), "par-rp", def, `{"orig":true}`)

	if exec.Status != driver.ExecStatusSucceeded {
		t.Fatalf("status = %q (err=%q), want SUCCEEDED", exec.Status, exec.Error)
	}

	if !jsonEqual(t, exec.Output, `{"orig":true,"results":[1,2]}`) {
		t.Fatalf("output = %q, want the array merged at $.results with siblings preserved", exec.Output)
	}
}

// TestMapPerItemOutputWithItemSelector runs a Map over an array and asserts the
// output is the per-iteration array with ItemSelector ($$.Map.Item.*) applied.
func TestMapPerItemOutputWithItemSelector(t *testing.T) {
	def := `{"StartAt":"M","States":{
		"M":{"Type":"Map","End":true,"ItemsPath":"$.nums","MaxConcurrency":3,
			"ItemSelector":{"value.$":"$$.Map.Item.Value","index.$":"$$.Map.Item.Index"},
			"ItemProcessor":{"StartAt":"I","States":{"I":{"Type":"Pass","End":true}}}}}}`

	exec := runSync(t, newMock(t), "map", def, `{"nums":[10,20,30]}`)

	if exec.Status != driver.ExecStatusSucceeded {
		t.Fatalf("status = %q (err=%q cause=%q), want SUCCEEDED", exec.Status, exec.Error, exec.Cause)
	}

	want := `[{"value":10,"index":0},{"value":20,"index":1},{"value":30,"index":2}]`
	if !jsonEqual(t, exec.Output, want) {
		t.Fatalf("output = %q, want %q", exec.Output, want)
	}
}

// TestMapIterationFailureFailsMap asserts one iteration failing fails the Map
// with that iteration's error when no Catcher handles it.
func TestMapIterationFailureFailsMap(t *testing.T) {
	def := `{"StartAt":"M","States":{
		"M":{"Type":"Map","End":true,"ItemsPath":"$.items",
			"ItemProcessor":{"StartAt":"C","States":{
				"C":{"Type":"Choice","Choices":[{"Variable":"$","NumericLessThan":3,"Next":"OK"}],"Default":"Boom"},
				"OK":{"Type":"Pass","End":true},
				"Boom":{"Type":"Fail","Error":"TooBig","Cause":"item exceeded the limit"}}}}}}`

	exec := runSync(t, newMock(t), "map-fail", def, `{"items":[1,2,5]}`)

	if exec.Status != driver.ExecStatusFailed {
		t.Fatalf("status = %q, want FAILED (iteration failure propagates)", exec.Status)
	}

	if exec.Error != "TooBig" {
		t.Fatalf("error = %q, want TooBig from the failing iteration", exec.Error)
	}
}

// TestMapResultSelectorAndResultPath asserts ResultSelector reshapes the Map
// result array and ResultPath splices it onto the raw input.
func TestMapResultSelectorAndResultPath(t *testing.T) {
	def := `{"StartAt":"M","States":{
		"M":{"Type":"Map","End":true,"ItemsPath":"$.n",
			"ResultSelector":{"items.$":"$"},
			"ResultPath":"$.mapped",
			"ItemProcessor":{"StartAt":"I","States":{"I":{"Type":"Pass","End":true}}}}}}`

	exec := runSync(t, newMock(t), "map-rs", def, `{"n":[1,2],"keep":9}`)

	if exec.Status != driver.ExecStatusSucceeded {
		t.Fatalf("status = %q (err=%q), want SUCCEEDED", exec.Status, exec.Error)
	}

	if !jsonEqual(t, exec.Output, `{"n":[1,2],"keep":9,"mapped":{"items":[1,2]}}`) {
		t.Fatalf("output = %q, want the reshaped result merged at $.mapped", exec.Output)
	}
}

// TestTaskResultPathFailureIsCaught is the Medium PR2-review fix: a Task whose
// ResultPath uses unsupported syntax fails with States.ResultPathMatchFailure —
// a state-internal I/O error that must now be CAUGHT by a matching Catcher rather
// than bypassing Catch and going straight to ExecutionFailed.
func TestTaskResultPathFailureIsCaught(t *testing.T) {
	inv := fakeLambda{fn: func(_ context.Context, _ string, _ []byte) ([]byte, string, error) {
		return []byte(`{"ok":1}`), "", nil
	}}

	def := `{"StartAt":"T","States":{
		"T":{"Type":"Task","Resource":"` + lambdaFuncARN + `","ResultPath":"$.a.b[0]","End":true,
			"Catch":[{"ErrorEquals":["States.ResultPathMatchFailure"],"Next":"Rescue","ResultPath":"$.caught"}]},
		"Rescue":{"Type":"Pass","End":true}}}`

	exec := runSync(t, taskMock(t, inv), "task-rp-catch", def, `{"in":1}`)

	if exec.Status != driver.ExecStatusSucceeded {
		t.Fatalf("status = %q (err=%q cause=%q), want SUCCEEDED via Catch — the ResultPath error must be catchable",
			exec.Status, exec.Error, exec.Cause)
	}

	if !jsonEqual(t, exec.Output,
		`{"in":1,"caught":{"Error":"States.ResultPathMatchFailure","Cause":"ResultPath \"$.a.b[0]\" uses unsupported syntax"}}`) {
		t.Fatalf("output = %q, want the raw input with the caught ResultPath error at $.caught", exec.Output)
	}
}

// TestParallelTaskLambdaRecursionBounded proves ctx (carrying recursion-guard
// depth) threads through a Parallel branch: a Parallel -> Task -> Lambda cycle
// that re-enters StartExecution terminates at recursionguard.MaxDepth with a
// bounded States.TaskFailed instead of overflowing the stack.
func TestParallelTaskLambdaRecursionBounded(t *testing.T) {
	m := newMock(t)

	def := `{"StartAt":"P","States":{"P":{"Type":"Parallel","End":true,
		"Branches":[{"StartAt":"T","States":{"T":{"Type":"Task","Resource":"` + lambdaFuncARN + `","End":true}}}]}}}`
	arn := createDef(t, m, "par-recurse", def)

	maxDepth := 0
	m.SetLambdaSyncInvoker(fakeLambda{fn: func(ctx context.Context, _ string, payload []byte) ([]byte, string, error) {
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
			return nil, sub.Error, nil
		}

		return []byte(sub.Output), "", nil
	}})

	top, err := m.StartExecution(context.Background(),
		driver.StartExecutionInput{StateMachineArn: arn, Name: "top", Input: `{}`})
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}

	if top.Status != driver.ExecStatusFailed || top.Error != "States.TaskFailed" {
		t.Fatalf("top terminal = %q/%q, want FAILED/States.TaskFailed (bounded recursion through Parallel branch)",
			top.Status, top.Error)
	}

	if maxDepth != recursionguard.MaxDepth {
		t.Fatalf("max ctx depth = %d, want %d (guard reached across the Parallel branch)",
			maxDepth, recursionguard.MaxDepth)
	}
}

// TestCreateRejectsPassRetryCatch is the Low PR2-review fix: Pass supports
// neither Retry nor Catch, so a definition using them is rejected at create time.
func TestCreateRejectsPassRetryCatch(t *testing.T) {
	cases := map[string]string{
		"retry on pass": `{"StartAt":"A","States":{"A":{"Type":"Pass",` +
			`"Retry":[{"ErrorEquals":["States.ALL"]}],"End":true}}}`,
		"catch on pass": `{"StartAt":"A","States":{"A":{"Type":"Pass",` +
			`"Catch":[{"ErrorEquals":["States.ALL"],"Next":"A"}],"End":true}}}`,
	}

	for name, def := range cases {
		err := createErr(t, def)
		if !errors.IsInvalidArgument(err) {
			t.Fatalf("%s: want InvalidArgument, got %v", name, err)
		}

		if exceptionOf(err) != driver.ExInvalidDefinition {
			t.Fatalf("%s: want InvalidDefinition, got %q (%v)", name, exceptionOf(err), err)
		}
	}
}

// TestCreateRejectsOutOfRangeRetrier is the Low PR2-review fix: Retrier fields are
// bounds-checked at create time (IntervalSeconds >= 0, MaxAttempts >= 0,
// BackoffRate >= 1.0).
func TestCreateRejectsOutOfRangeRetrier(t *testing.T) {
	arn := lambdaFuncARN
	cases := map[string]string{
		"backoff below 1": `{"StartAt":"T","States":{"T":{"Type":"Task","Resource":"` + arn + `",` +
			`"Retry":[{"ErrorEquals":["States.ALL"],"BackoffRate":0.5}],"End":true}}}`,
		"negative interval": `{"StartAt":"T","States":{"T":{"Type":"Task","Resource":"` + arn + `",` +
			`"Retry":[{"ErrorEquals":["States.ALL"],"IntervalSeconds":-1}],"End":true}}}`,
		"negative maxattempts": `{"StartAt":"T","States":{"T":{"Type":"Task","Resource":"` + arn + `",` +
			`"Retry":[{"ErrorEquals":["States.ALL"],"MaxAttempts":-2}],"End":true}}}`,
	}

	for name, def := range cases {
		err := createErr(t, def)
		if !errors.IsInvalidArgument(err) {
			t.Fatalf("%s: want InvalidArgument, got %v", name, err)
		}

		if exceptionOf(err) != driver.ExInvalidDefinition {
			t.Fatalf("%s: want InvalidDefinition, got %q (%v)", name, exceptionOf(err), err)
		}
	}
}

// TestParallelMapHistoryEvents asserts the nested branch/iteration state events
// appear inline between the Parallel/Map enter and exit, with a valid chain.
func TestParallelMapHistoryEvents(t *testing.T) {
	def := `{"StartAt":"P","States":{
		"P":{"Type":"Parallel","End":true,"Branches":[
			{"StartAt":"A","States":{"A":{"Type":"Pass","End":true}}}
		]}}}`

	m := newMock(t)
	exec := runSync(t, m, "par-hist", def, `{}`)

	events, err := m.GetExecutionHistory(context.Background(), exec.ARN, false)
	if err != nil {
		t.Fatalf("GetExecutionHistory: %v", err)
	}

	want := []string{
		"ExecutionStarted",
		"ParallelStateEntered",
		"PassStateEntered", "PassStateExited",
		"ParallelStateExited",
		"ExecutionSucceeded",
	}

	if len(events) != len(want) {
		t.Fatalf("history has %d events, want %d: %+v", len(events), len(want), events)
	}

	for i, w := range want {
		if events[i].Type != w {
			t.Fatalf("event %d type = %q, want %q", i, events[i].Type, w)
		}
	}
}
