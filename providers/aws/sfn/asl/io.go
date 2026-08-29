package asl

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The I/O pipeline stages, applied per state only where ASL permits:
//
//	raw -> InputPath -> Parameters -> [work -> Result] -> ResultSelector -> ResultPath -> OutputPath
//
// ResultPath's merge base is the RAW input handed to the state (before
// InputPath), so InputPath-narrow + ResultPath-reattach preserves the sibling
// fields InputPath excluded.

// applyInputPath selects the sub-document a state operates on. Absent InputPath
// means the whole input ("$"); an explicit null yields an empty object.
func (it *interp) applyInputPath(st *State, raw any) (any, error) {
	if !st.InputPath.set {
		return raw, nil
	}

	if st.InputPath.null {
		return map[string]any{}, nil
	}

	v, present, err := it.resolvePath(st.InputPath.path, raw)
	if err != nil {
		return nil, err
	}

	if !present {
		return nil, &stateError{Code: "States.ParameterPathFailure",
			Cause: fmt.Sprintf("InputPath %q could not be found in the input", st.InputPath.path)}
	}

	return v, nil
}

// applyParameters resolves the Parameters payload template against the filtered
// input, producing the payload the state work receives.
func (it *interp) applyParameters(st *State, input any) (any, error) {
	if st.Parameters == nil {
		return input, nil
	}

	return it.applyPayloadTemplate(st.Parameters, input)
}

// applyResultSelector reshapes a Task/Parallel/Map result via a payload template
// before ResultPath merges it. Absent ResultSelector passes the result through.
func (it *interp) applyResultSelector(st *State, result any) (any, error) {
	if st.ResultSelector == nil {
		return result, nil
	}

	return it.applyPayloadTemplate(st.ResultSelector, result)
}

// applyResultPath merges the result onto the RAW input using the state's
// ResultPath. Absent ResultPath defaults to "$" (the result replaces the
// document); an explicit null discards the result and passes the raw input
// through; a path splices the result in.
func (*interp) applyResultPath(st *State, raw, result any) (any, error) {
	return mergeResultPath(st.ResultPath, raw, result)
}

// mergeResultPath splices result into raw at the given ResultPath (the shared
// semantic used by both a state's ResultPath and a Catcher's ResultPath).
func mergeResultPath(rp pathField, raw, result any) (any, error) {
	if !rp.set || rp.path == "$" {
		return result, nil
	}

	if rp.null {
		return raw, nil
	}

	fields, err := objectFieldPath(rp.path)
	if err != nil {
		return nil, err
	}

	return spliceFields(deepCopy(raw), fields, result), nil
}

// applyOutputPath selects the effective output. Absent OutputPath means the
// whole document ("$"); an explicit null yields an empty object.
func (it *interp) applyOutputPath(st *State, doc any) (any, error) {
	if !st.OutputPath.set {
		return doc, nil
	}

	if st.OutputPath.null {
		return map[string]any{}, nil
	}

	v, present, err := it.resolvePath(st.OutputPath.path, doc)
	if err != nil {
		return nil, err
	}

	if !present {
		return nil, &stateError{Code: "States.OutputMatchFailure",
			Cause: fmt.Sprintf("OutputPath %q could not be found in the output", st.OutputPath.path)}
	}

	return v, nil
}

// stateInput runs the input side of the pipeline (InputPath -> Parameters),
// producing the value the state's work receives. Errors are returned as
// *stateError so a Task/Parallel/Map I/O failure feeds Retry/Catch.
func (it *interp) stateInput(st *State, raw any) (any, *stateError) {
	filtered, err := it.applyInputPath(st, raw)
	if err != nil {
		return nil, asStateError(err)
	}

	params, err := it.applyParameters(st, filtered)
	if err != nil {
		return nil, asStateError(err)
	}

	return params, nil
}

// resultPipeline runs the result side of the pipeline (ResultSelector ->
// ResultPath onto the RAW input -> OutputPath) on a state's work result. Errors
// are returned as *stateError so they are catchable, consistent with Task.
func (it *interp) resultPipeline(st *State, raw, result any) (any, *stateError) {
	selected, err := it.applyResultSelector(st, result)
	if err != nil {
		return nil, asStateError(err)
	}

	merged, err := it.applyResultPath(st, raw, selected)
	if err != nil {
		return nil, asStateError(err)
	}

	out, err := it.applyOutputPath(st, merged)
	if err != nil {
		return nil, asStateError(err)
	}

	return out, nil
}

// catchOrFail routes a state failure to a matching Catcher (transitioning to its
// Next with the {Error,Cause} error output merged at its ResultPath), or
// propagates it to ExecutionFailed when no Catcher matches. It is shared by
// Task/Parallel/Map, so their own I/O-pipeline errors are catchable exactly like
// their work failures.
func catchOrFail(st *State, raw any, se *stateError) (out any, next string, terminal bool, err error) {
	if caughtOut, catchNext, ok := tryCatch(st, raw, se); ok {
		return caughtOut, catchNext, false, nil
	}

	return nil, "", false, se
}

// passThroughOutput is the output pipeline for states with no result to merge
// (Choice, Wait, Succeed): the effective output is OutputPath applied to the
// InputPath-filtered input.
func (it *interp) passThroughOutput(st *State, raw any) (any, error) {
	in, err := it.applyInputPath(st, raw)
	if err != nil {
		return nil, err
	}

	return it.applyOutputPath(st, in)
}

// objectFieldPath parses a ResultPath into its object field names. Only the
// reference subset "$.a.b" is supported; an index or unsupported syntax is a
// loud States.ResultPathMatchFailure rather than a wrong silent merge.
func objectFieldPath(path string) ([]string, error) {
	if !strings.HasPrefix(path, "$") {
		return nil, &stateError{Code: "States.ResultPathMatchFailure",
			Cause: fmt.Sprintf("ResultPath %q is not a valid reference path", path)}
	}

	rest := strings.TrimPrefix(path, "$")
	rest = strings.TrimPrefix(rest, ".")

	if rest == "" {
		return nil, nil
	}

	fields := strings.Split(rest, ".")
	for _, f := range fields {
		if f == "" || strings.ContainsAny(f, "[]*?@") {
			return nil, &stateError{Code: "States.ResultPathMatchFailure",
				Cause: fmt.Sprintf("ResultPath %q uses unsupported syntax", path)}
		}
	}

	return fields, nil
}

// spliceFields returns doc with result set at the nested object path fields,
// creating intermediate objects as needed.
func spliceFields(doc any, fields []string, result any) any {
	if len(fields) == 0 {
		return result
	}

	m, ok := doc.(map[string]any)
	if !ok {
		m = map[string]any{}
	}

	m[fields[0]] = spliceFields(m[fields[0]], fields[1:], result)

	return m
}

// deepCopy clones a JSON value so a ResultPath merge never mutates the raw input.
func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = deepCopy(val)
		}

		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = deepCopy(val)
		}

		return out
	default:
		return v
	}
}

// jsonNumberOrValue unmarshals a raw JSON value into a Go value.
func rawToValue(raw json.RawMessage) (any, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}

	return v, nil
}
