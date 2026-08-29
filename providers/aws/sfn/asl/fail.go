package asl

import "context"

// failHandler runs a Fail state: it emits a FailStateEntered event (terminal, so
// no exit) and returns a stateError carrying the state's Error/Cause, which the
// walker records as ExecutionFailed.
func failHandler(_ context.Context, it *interp, st *State, _ any) (out any, next string, terminal bool, err error) {
	it.emit(historyFail(st))

	return nil, "", false, &stateError{Code: st.Error, Cause: st.Cause}
}
