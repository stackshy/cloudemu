package asl

import "github.com/stackshy/cloudemu/v2/services/sfn/driver"

// The event history is accumulated by interp.emit (interpret.go), which assigns
// the monotonic ID, chains PreviousEventID, and stamps the virtual timestamp.
// The event set for this build is:
//
//	ExecutionStarted
//	<Type>StateEntered / <Type>StateExited   (Pass, Choice, Wait, Succeed)
//	FailStateEntered                          (Fail — terminal, no exit)
//	ExecutionSucceeded / ExecutionFailed
//
// Richer AWS sub-events (LambdaFunction*, MapIteration*, evaluation events) are
// out of scope until the Task/Parallel/Map handlers land.

// historyFail builds the FailStateEntered event for a Fail state.
func historyFail(st *State) *driver.HistoryEvent {
	return &driver.HistoryEvent{
		Type:      "FailStateEntered",
		StateName: st.name,
		Error:     st.Error,
		Cause:     st.Cause,
	}
}
