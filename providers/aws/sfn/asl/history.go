package asl

import "github.com/stackshy/cloudemu/v2/services/sfn/driver"

// The event history is accumulated by interp.emit (interpret.go), which assigns
// the monotonic ID, chains PreviousEventID, and stamps the virtual timestamp.
// The event set for this build is:
//
//	ExecutionStarted
//	<Type>StateEntered / <Type>StateExited   (Pass, Choice, Wait, Succeed, Task,
//	                                           Parallel, Map)
//	FailStateEntered                          (Fail — terminal, no exit)
//	LambdaFunctionScheduled / LambdaFunctionStarted /
//	LambdaFunctionSucceeded / LambdaFunctionFailed   (Task->Lambda sub-events)
//	ExecutionSucceeded / ExecutionFailed / ExecutionAborted
//
// A Parallel/Map's nested branch/iteration states emit their own
// <Type>StateEntered/Exited events inline between the Parallel/Map enter and
// exit. Richer AWS sub-events (MapIterationStarted/Succeeded, per-branch
// evaluation events) are out of scope.

// historyFail builds the FailStateEntered event for a Fail state.
func historyFail(st *State) *driver.HistoryEvent {
	return &driver.HistoryEvent{
		Type:      "FailStateEntered",
		StateName: st.name,
		Error:     st.Error,
		Cause:     st.Cause,
	}
}

// historyLambdaScheduled builds the LambdaFunctionScheduled event carrying the
// Task Resource and the payload delivered to the function.
func historyLambdaScheduled(st *State, payload []byte) *driver.HistoryEvent {
	return &driver.HistoryEvent{Type: "LambdaFunctionScheduled", Resource: st.Resource, Input: string(payload)}
}

// historyLambdaSucceeded builds the LambdaFunctionSucceeded event carrying the
// task result.
func historyLambdaSucceeded(result any) *driver.HistoryEvent {
	return &driver.HistoryEvent{Type: "LambdaFunctionSucceeded", Output: toJSON(result)}
}

// historyLambdaFailed builds the LambdaFunctionFailed event carrying the error
// code and cause that surface to Retry/Catch.
func historyLambdaFailed(se *stateError) *driver.HistoryEvent {
	return &driver.HistoryEvent{Type: "LambdaFunctionFailed", Error: se.Code, Cause: se.Cause}
}
