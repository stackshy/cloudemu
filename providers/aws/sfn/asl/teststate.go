package asl

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

// StateResult is the outcome of evaluating a single state via TestOne.
type StateResult struct {
	Output    string
	NextState string
	Status    string
	Error     string
	Cause     string
}

// testStateName is the synthetic name a TestOne single state is run under.
const testStateName = "__TestState__"

// TestOne evaluates one bare ASL state definition against an input, running its
// handler exactly once (it does not follow Next). It returns the state's output,
// the Next it would transition to (the selected branch for a Choice; empty for a
// terminal state), and SUCCEEDED/FAILED. A structurally-invalid state is a
// parse error the caller maps to its own validation surface.
func TestOne(ctx context.Context, definition string, in *RunInput) (*StateResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	var st State
	if err := json.Unmarshal([]byte(definition), &st); err != nil {
		return nil, fmt.Errorf("state definition is not valid JSON: %w", err)
	}

	if err := validateSingleState(&st); err != nil {
		return nil, err
	}

	st.name = testStateName

	it := &interp{
		baseTime: in.StartTime, offset: in.SettleBase, maxSteps: 1,
		handlers: buildHandlers(), invokeLambda: in.InvokeLambda,
	}
	input := parseInput(in.Input)
	it.buildContext(in, input)
	it.enterStateContext(&st)

	out, next, terminal, err := it.handlers[st.Type](ctx, it, &st, input)
	if err != nil {
		se := asStateError(err)

		return &StateResult{Status: driver.TestStatusFailed, Error: se.Code, Cause: se.Cause}, nil
	}

	res := &StateResult{Status: driver.TestStatusSucceeded, Output: toJSON(out)}
	if !terminal {
		res.NextState = next
	}

	return res, nil
}

// validateSingleState applies the state-level checks that do not depend on the
// surrounding States map (Next-target existence is not checkable for one state).
func validateSingleState(st *State) error {
	if st.Type == "" {
		return aslErrf("state is missing the required 'Type' field")
	}

	if !knownType(st.Type) {
		return aslErrf("unknown state Type %q", st.Type)
	}

	if st.QueryLanguage == queryLangJSONata {
		return aslErrf("QueryLanguage %q is not supported (JSONPath only)", st.QueryLanguage)
	}

	return validateFieldApplicability(st)
}
