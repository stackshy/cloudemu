package asl

import "github.com/stackshy/cloudemu/v2/internal/jsonpath"

// evalPath evaluates a JSONPath reference against root, returning the selected
// value and whether it was present. The supported subset lives in
// internal/jsonpath; its errors surface as ASL definition errors.
func evalPath(path string, root any) (value any, present bool, err error) {
	v, ok, err := jsonpath.Eval(path, root)
	if err != nil {
		return nil, false, aslErrf("%s", err.Error())
	}

	return v, ok, nil
}
