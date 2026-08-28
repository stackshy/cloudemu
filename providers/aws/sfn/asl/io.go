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

// applyResultPath merges the result onto the RAW input. Absent ResultPath
// defaults to "$" (the result replaces the document); an explicit null discards
// the result and passes the raw input through; a path splices the result in.
func (*interp) applyResultPath(st *State, raw, result any) (any, error) {
	if !st.ResultPath.set || st.ResultPath.path == "$" {
		return result, nil
	}

	if st.ResultPath.null {
		return raw, nil
	}

	fields, err := objectFieldPath(st.ResultPath.path)
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
