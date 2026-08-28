package asl

import "context"

// succeedHandler runs a Succeed state: it terminates the execution SUCCEEDED,
// passing the InputPath/OutputPath-filtered input through as the output.
func succeedHandler(_ context.Context, it *interp, st *State, raw any) (out any, next string, terminal bool, err error) {
	it.enterState(st, raw)

	out, err = it.passThroughOutput(st, raw)
	if err != nil {
		return nil, "", false, err
	}

	it.exitState(st, out)

	return out, "", true, nil
}
