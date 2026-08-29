package sfn_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

// createErr attempts to create a state machine and returns the error.
func createErr(t *testing.T, def string) error {
	t.Helper()

	m := newMock(t)
	_, _, _, err := m.CreateStateMachine(context.Background(), driver.CreateStateMachineInput{
		Name: "sm", Definition: def, RoleArn: "arn:aws:iam::000000000000:role/r",
	})

	return err
}

// TestCreateRejectsInvalidDefinitions asserts the stricter parser rejects a
// range of malformed ASL at CreateStateMachine time with InvalidDefinition.
func TestCreateRejectsInvalidDefinitions(t *testing.T) {
	cases := map[string]string{
		"jsonata top-level":     `{"QueryLanguage":"JSONata","StartAt":"A","States":{"A":{"Type":"Pass","End":true}}}`,
		"jsonata per-state":     `{"StartAt":"A","States":{"A":{"Type":"Pass","QueryLanguage":"JSONata","End":true}}}`,
		"unknown state type":    `{"StartAt":"A","States":{"A":{"Type":"Frobnicate","End":true}}}`,
		"dangling next":         `{"StartAt":"A","States":{"A":{"Type":"Pass","Next":"Nowhere"}}}`,
		"missing next and end":  `{"StartAt":"A","States":{"A":{"Type":"Pass"}}}`,
		"choice without rules":  `{"StartAt":"A","States":{"A":{"Type":"Choice","Choices":[]}}}`,
		"params on wait":        `{"StartAt":"A","States":{"A":{"Type":"Wait","Seconds":1,"Parameters":{"x":1},"End":true}}}`,
		"resultpath on succeed": `{"StartAt":"A","States":{"A":{"Type":"Succeed","ResultPath":"$.r"}}}`,
		"missing startat":       `{"States":{"A":{"Type":"Pass","End":true}}}`,
		"not json":              `{not json`,
	}

	for name, def := range cases {
		err := createErr(t, def)
		if !errors.IsInvalidArgument(err) {
			t.Fatalf("%s: want InvalidArgument, got %v", name, err)
		}

		if exceptionOf(err) != driver.ExInvalidDefinition {
			t.Fatalf("%s: want InvalidDefinition exception, got %q (%v)", name, exceptionOf(err), err)
		}
	}
}

// TestValidateStateMachineDefinitionRejects asserts the same structural checks
// surface as FAIL diagnostics through ValidateStateMachineDefinition.
func TestValidateStateMachineDefinitionRejects(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	bad := []string{
		`{"QueryLanguage":"JSONata","StartAt":"A","States":{"A":{"Type":"Pass","End":true}}}`,
		`{"StartAt":"A","States":{"A":{"Type":"Choice","Choices":[]}}}`,
		`{"StartAt":"A","States":{"A":{"Type":"Pass","Next":"Missing"}}}`,
	}

	for _, def := range bad {
		res, err := m.ValidateStateMachineDefinition(ctx, def, driver.TypeStandard)
		if err != nil {
			t.Fatalf("ValidateStateMachineDefinition returned error: %v", err)
		}

		if res.Result != driver.ValidationResultFail || len(res.Diagnostics) == 0 {
			t.Fatalf("definition %q should FAIL with a diagnostic, got %+v", def, res)
		}
	}

	good := `{"StartAt":"A","States":{"A":{"Type":"Pass","End":true}}}`
	res, _ := m.ValidateStateMachineDefinition(ctx, good, driver.TypeStandard)
	if res.Result != driver.ValidationResultOK {
		t.Fatalf("valid definition should be OK, got %+v", res)
	}
}

// TestUnsupportedJSONPathFailsLoudly asserts that a definition using JSONPath
// syntax outside the supported subset (here a wildcard) fails the execution
// loudly instead of silently returning a wrong result.
func TestUnsupportedJSONPathFailsLoudly(t *testing.T) {
	def := `{"StartAt":"C","States":{
		"C":{"Type":"Choice","Choices":[{"Variable":"$.items.*","IsPresent":true,"Next":"S"}],"Default":"S"},
		"S":{"Type":"Succeed"}}}`

	exec := runSync(t, newMock(t), "badpath", def, `{"items":[1,2]}`)

	if exec.Status != driver.ExecStatusFailed {
		t.Fatalf("status = %q, want FAILED for unsupported JSONPath", exec.Status)
	}
}

// TestUnsupportedStateTypeFailsLoudly asserts a Task/Parallel/Map state (valid
// ASL, accepted at create time) fails loudly at run time until its handler lands.
func TestUnsupportedStateTypeFailsLoudly(t *testing.T) {
	def := `{"StartAt":"T","States":{"T":{"Type":"Task",
		"Resource":"arn:aws:states:::lambda:invoke","End":true}}}`

	// Accepted at create time (valid ASL).
	if err := createErr(t, def); err != nil {
		t.Fatalf("Task definition should be accepted at create time, got %v", err)
	}

	exec := runSync(t, newMock(t), "task", def, `{}`)
	if exec.Status != driver.ExecStatusFailed {
		t.Fatalf("Task run status = %q, want FAILED (unsupported until PR2)", exec.Status)
	}
}

// TestIdempotentDuplicateCreate asserts two identical CreateStateMachine calls
// (verbatim definition) return the same machine rather than AlreadyExists.
func TestIdempotentDuplicateCreate(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	def := `{"StartAt":"A","States":{"A":{"Type":"Pass","End":true}}}`

	in := driver.CreateStateMachineInput{Name: "dup", Definition: def, RoleArn: "arn:aws:iam::000000000000:role/r"}

	arn1, _, _, err := m.CreateStateMachine(ctx, in)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	arn2, _, _, err := m.CreateStateMachine(ctx, in)
	if err != nil {
		t.Fatalf("idempotent duplicate create should succeed, got %v", err)
	}

	if arn1 != arn2 {
		t.Fatalf("duplicate create returned a different ARN: %q vs %q", arn1, arn2)
	}
}
