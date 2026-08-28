package asl

import (
	"encoding/json"
	"fmt"
	"strings"
)

// applyPayloadTemplate resolves a Parameters/ResultSelector payload template
// against input: object keys ending in ".$" take a JSONPath ("$"/"$$") or an
// intrinsic string and are rewritten to the stripped key with the resolved
// value; all other values are passed through (recursing into objects/arrays).
func (it *interp) applyPayloadTemplate(tmpl json.RawMessage, input any) (any, error) {
	var node any
	if err := json.Unmarshal(tmpl, &node); err != nil {
		return nil, err
	}

	return it.resolveTemplateNode(node, input)
}

func (it *interp) resolveTemplateNode(node, input any) (any, error) {
	switch n := node.(type) {
	case map[string]any:
		return it.resolveTemplateMap(n, input)
	case []any:
		out := make([]any, len(n))

		for i, v := range n {
			rv, err := it.resolveTemplateNode(v, input)
			if err != nil {
				return nil, err
			}

			out[i] = rv
		}

		return out, nil
	default:
		return node, nil
	}
}

func (it *interp) resolveTemplateMap(n map[string]any, input any) (any, error) {
	out := make(map[string]any, len(n))

	for k, v := range n {
		if !strings.HasSuffix(k, ".$") {
			rv, err := it.resolveTemplateNode(v, input)
			if err != nil {
				return nil, err
			}

			out[k] = rv

			continue
		}

		s, ok := v.(string)
		if !ok {
			return nil, aslErrf("payload template field %q must be a string", k)
		}

		rv, err := it.resolveTemplateString(s, input)
		if err != nil {
			return nil, err
		}

		out[strings.TrimSuffix(k, ".$")] = rv
	}

	return out, nil
}

// resolveTemplateString resolves a ".$" template value: an intrinsic invocation
// (States.*), or a JSONPath against the input ($) or context object ($$).
func (it *interp) resolveTemplateString(s string, input any) (any, error) {
	if strings.HasPrefix(s, "States.") {
		return it.evalIntrinsic(s, input)
	}

	if strings.HasPrefix(s, "$") {
		v, present, err := it.resolvePath(s, input)
		if err != nil {
			return nil, err
		}

		if !present {
			return nil, &stateError{Code: "States.ParameterPathFailure",
				Cause: fmt.Sprintf("payload template path %q could not be found", s)}
		}

		return v, nil
	}

	return nil, aslErrf("payload template value %q must be a JSONPath or an intrinsic", s)
}
