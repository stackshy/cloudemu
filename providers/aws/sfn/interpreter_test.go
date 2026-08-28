package sfn_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/stackshy/cloudemu/v2/providers/aws/sfn"
	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

// createDef registers a state machine with the given verbatim ASL definition.
func createDef(t *testing.T, m *sfn.Mock, name, def string) string {
	t.Helper()

	arn, _, _, err := m.CreateStateMachine(context.Background(), driver.CreateStateMachineInput{
		Name: name, Definition: def, RoleArn: "arn:aws:iam::000000000000:role/r",
	})
	if err != nil {
		t.Fatalf("CreateStateMachine(%s): %v", name, err)
	}

	return arn
}

// runSync creates a machine and runs one synchronous (instant) execution.
func runSync(t *testing.T, m *sfn.Mock, name, def, input string) *driver.Execution {
	t.Helper()

	arn := createDef(t, m, name, def)

	exec, err := m.StartSyncExecution(context.Background(), driver.StartExecutionInput{
		StateMachineArn: arn, Name: "run", Input: input,
	})
	if err != nil {
		t.Fatalf("StartSyncExecution(%s): %v", name, err)
	}

	return exec
}

// jsonEqual reports whether two JSON strings are semantically equal.
func jsonEqual(t *testing.T, got, want string) bool {
	t.Helper()

	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("got is not JSON: %q (%v)", got, err)
	}

	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("want is not JSON: %q (%v)", want, err)
	}

	return reflect.DeepEqual(g, w)
}

// TestInterpreterPassWaitChoiceSucceed runs a Pass -> Wait -> Choice -> Succeed
// machine end to end and asserts the terminal status and output document.
func TestInterpreterPassWaitChoiceSucceed(t *testing.T) {
	def := `{"StartAt":"P","States":{
		"P":{"Type":"Pass","Result":{"step":1},"ResultPath":"$.p","Next":"W"},
		"W":{"Type":"Wait","Seconds":0,"Next":"C"},
		"C":{"Type":"Choice","Choices":[{"Variable":"$.p.step","NumericEquals":1,"Next":"S"}],"Default":"F"},
		"S":{"Type":"Succeed"},
		"F":{"Type":"Fail","Error":"Bad","Cause":"unexpected"}}}`

	exec := runSync(t, newMock(t), "pwcs", def, `{"x":10}`)

	if exec.Status != driver.ExecStatusSucceeded {
		t.Fatalf("status = %q (err=%q cause=%q), want SUCCEEDED", exec.Status, exec.Error, exec.Cause)
	}

	if !jsonEqual(t, exec.Output, `{"x":10,"p":{"step":1}}`) {
		t.Fatalf("output = %q, want the input with p.step reattached", exec.Output)
	}
}

// TestInterpreterFailState asserts a Fail state terminates FAILED with Error/Cause.
func TestInterpreterFailState(t *testing.T) {
	def := `{"StartAt":"F","States":{"F":{"Type":"Fail","Error":"MyError","Cause":"my cause"}}}`

	exec := runSync(t, newMock(t), "failmc", def, `{}`)

	if exec.Status != driver.ExecStatusFailed || exec.Error != "MyError" || exec.Cause != "my cause" {
		t.Fatalf("Fail terminal = %+v, want FAILED/MyError/my cause", exec)
	}
}

// TestInterpreterChoiceComparatorFamilies drives one branch per comparator
// family (string, numeric, boolean, timestamp, type-check, StringMatches glob)
// plus the Default fall-through.
func TestInterpreterChoiceComparatorFamilies(t *testing.T) {
	def := `{"StartAt":"C","States":{
		"C":{"Type":"Choice","Choices":[
			{"Variable":"$.s","StringEquals":"hello","Next":"Str"},
			{"Variable":"$.n","NumericGreaterThan":10,"Next":"Num"},
			{"Variable":"$.b","BooleanEquals":true,"Next":"Bool"},
			{"Variable":"$.t","TimestampLessThan":"2020-01-01T00:00:00Z","Next":"Ts"},
			{"Variable":"$.p","IsPresent":true,"Next":"Present"},
			{"Variable":"$.m","StringMatches":"foo*bar","Next":"Match"}
		],"Default":"Def"},
		"Str":{"Type":"Pass","Result":"str","End":true},
		"Num":{"Type":"Pass","Result":"num","End":true},
		"Bool":{"Type":"Pass","Result":"bool","End":true},
		"Ts":{"Type":"Pass","Result":"ts","End":true},
		"Present":{"Type":"Pass","Result":"present","End":true},
		"Match":{"Type":"Pass","Result":"match","End":true},
		"Def":{"Type":"Pass","Result":"def","End":true}}}`

	cases := []struct {
		input string
		want  string
	}{
		{`{"s":"hello"}`, `"str"`},
		{`{"n":42}`, `"num"`},
		{`{"b":true}`, `"bool"`},
		{`{"t":"2019-06-01T00:00:00Z"}`, `"ts"`},
		{`{"p":"x"}`, `"present"`},
		{`{"m":"fooXXbar"}`, `"match"`},
		{`{"other":1}`, `"def"`},
	}

	for i, tc := range cases {
		m := newMock(t)
		exec := runSync(t, m, fmt.Sprintf("choice-%d", i), def, tc.input)

		if exec.Status != driver.ExecStatusSucceeded {
			t.Fatalf("case %d status = %q err=%q", i, exec.Status, exec.Error)
		}

		if !jsonEqual(t, exec.Output, tc.want) {
			t.Fatalf("case %d input %s output = %q, want %q", i, tc.input, exec.Output, tc.want)
		}
	}
}

// TestInterpreterResultPathMergesOntoRawInput is the corrected-semantic guard:
// InputPath narrows the state's view, but ResultPath reattaches the result onto
// the RAW input, so the sibling fields InputPath excluded survive in the output.
func TestInterpreterResultPathMergesOntoRawInput(t *testing.T) {
	def := `{"StartAt":"T","States":{"T":{"Type":"Pass",
		"InputPath":"$.detail","Result":{"ok":true},"ResultPath":"$.result","End":true}}}`

	exec := runSync(t, newMock(t), "rp-merge", def, `{"detail":{"id":1},"meta":"keep"}`)

	// meta was excluded by InputPath but must survive, because ResultPath merges
	// onto the RAW input, not the InputPath-filtered document.
	want := `{"detail":{"id":1},"meta":"keep","result":{"ok":true}}`
	if !jsonEqual(t, exec.Output, want) {
		t.Fatalf("output = %q, want %q (sibling 'meta' preserved via raw-input merge)", exec.Output, want)
	}
}

// TestInterpreterResultPathNullAndDefault pins the two other ResultPath modes:
// null passes the RAW input through unchanged (discarding the result), and the
// default '$' replaces the document with the result.
func TestInterpreterResultPathNullAndDefault(t *testing.T) {
	null := `{"StartAt":"T","States":{"T":{"Type":"Pass",
		"InputPath":"$.detail","Result":{"ignored":true},"ResultPath":null,"End":true}}}`
	exec := runSync(t, newMock(t), "rp-null", null, `{"detail":{"id":1},"meta":"keep"}`)

	if !jsonEqual(t, exec.Output, `{"detail":{"id":1},"meta":"keep"}`) {
		t.Fatalf("ResultPath:null output = %q, want the raw input passed through", exec.Output)
	}

	def := `{"StartAt":"T","States":{"T":{"Type":"Pass","Result":{"x":1},"End":true}}}`
	exec = runSync(t, newMock(t), "rp-default", def, `{"meta":"gone"}`)

	if !jsonEqual(t, exec.Output, `{"x":1}`) {
		t.Fatalf("default ResultPath output = %q, want the result replacing the document", exec.Output)
	}
}

// TestInterpreterOutputPathFilters asserts OutputPath narrows the effective output.
func TestInterpreterOutputPathFilters(t *testing.T) {
	def := `{"StartAt":"T","States":{"T":{"Type":"Pass","OutputPath":"$.detail","End":true}}}`

	exec := runSync(t, newMock(t), "outpath", def, `{"detail":{"id":9},"meta":"x"}`)

	if !jsonEqual(t, exec.Output, `{"id":9}`) {
		t.Fatalf("OutputPath output = %q, want just the selected sub-document", exec.Output)
	}
}
