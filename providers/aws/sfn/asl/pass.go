package asl

import (
	"context"
	"encoding/json"
	"fmt"
)

// passHandler runs a Pass state: InputPath -> Parameters produce the effective
// input; the result is the explicit Result field when present, else that input;
// then ResultPath (onto the raw input) -> OutputPath produce the output.
func passHandler(_ context.Context, it *interp, st *State, raw any) (out any, next string, terminal bool, err error) {
	it.enterState(st, raw)

	filtered, err := it.applyInputPath(st, raw)
	if err != nil {
		return nil, "", false, err
	}

	filtered, err = it.applyParameters(st, filtered)
	if err != nil {
		return nil, "", false, err
	}

	result := filtered
	if st.Result != nil {
		if uerr := json.Unmarshal(st.Result, &result); uerr != nil {
			return nil, "", false, fmt.Errorf("pass state %q Result is not valid JSON: %w", st.name, uerr)
		}
	}

	merged, err := it.applyResultPath(st, raw, result)
	if err != nil {
		return nil, "", false, err
	}

	out, err = it.applyOutputPath(st, merged)
	if err != nil {
		return nil, "", false, err
	}

	it.exitState(st, out)

	return out, st.Next, st.End, nil
}

// unsupportedHandler fails an execution loudly when it reaches a state type
// whose interpreter has not landed yet (Task, Parallel, Map).
func unsupportedHandler(_ context.Context, _ *interp, st *State, _ any) (out any, next string, terminal bool, err error) {
	return nil, "", false, &stateError{
		Code:  "States.Runtime",
		Cause: fmt.Sprintf("state type %q is not yet supported by the interpreter", st.Type),
	}
}
