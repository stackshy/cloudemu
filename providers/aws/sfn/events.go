package sfn

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/awsevents"
	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

// EventBridge identity of Step Functions' native execution event. Real Step
// Functions publishes it for STANDARD workflows only. See "Automating Step
// Functions event delivery with EventBridge" in the Step Functions guide.
const (
	eventSource                = "aws.states"
	eventExecutionStatusChange = "Step Functions Execution Status Change"
)

// executionStatusChangeDetail is the detail payload of a
// "Step Functions Execution Status Change" event. Fields that are not yet
// known for the status (stopDate/output while RUNNING, error/cause on
// success) are JSON null, as in the real event.
type executionStatusChangeDetail struct {
	ExecutionArn    string          `json:"executionArn"`
	StateMachineArn string          `json:"stateMachineArn"`
	Name            string          `json:"name"`
	Status          string          `json:"status"`
	StartDate       int64           `json:"startDate"`
	StopDate        *int64          `json:"stopDate"`
	Input           string          `json:"input"`
	InputDetails    payloadDetails  `json:"inputDetails"`
	Output          *string         `json:"output"`
	OutputDetails   *payloadDetails `json:"outputDetails"`
	Error           *string         `json:"error"`
	Cause           *string         `json:"cause"`
	// Redrive fields (see ExecutionRedriveStatus). redriveDate and
	// redriveStatusReason are null until they apply.
	RedriveCount        int32   `json:"redriveCount"`
	RedriveDate         *int64  `json:"redriveDate"`
	RedriveStatus       string  `json:"redriveStatus"`
	RedriveStatusReason *string `json:"redriveStatusReason"`
}

type payloadDetails struct {
	Included bool `json:"included"`
}

// SetEventPublisher wires the EventBridge default bus that execution status
// changes are published to. Safe to leave unset — no events are emitted.
func (m *Mock) SetEventPublisher(p awsevents.Publisher) {
	m.events.SetPublisher(p)
}

// emitExecutionStatus publishes a status-change event for exec as it stands at
// the given status. It must be called without the execution's lock held.
func (m *Mock) emitExecutionStatus(ctx context.Context, exec *driver.Execution, status string) {
	d := executionStatusChangeDetail{
		ExecutionArn: exec.ARN, StateMachineArn: exec.StateMachineArn, Name: exec.Name,
		Status: status, StartDate: exec.StartDate.UnixMilli(),
		Input: emptyOr(exec.Input), InputDetails: payloadDetails{Included: true},
		RedriveCount: exec.RedriveCount, RedriveDate: epochMillis(exec.RedriveDate),
	}

	redriveStatus, reason := driver.ExecutionRedriveStatus(status)
	d.RedriveStatus, d.RedriveStatusReason = redriveStatus, nonEmpty(reason)

	if status != driver.ExecStatusRunning {
		d.StopDate = epochMillis(exec.StopDate)
		d.Error = nonEmpty(exec.Error)
		d.Cause = nonEmpty(exec.Cause)

		if status == driver.ExecStatusSucceeded {
			out := emptyOr(exec.Output)
			d.Output = &out
			d.OutputDetails = &payloadDetails{Included: true}
		}
	}

	m.events.Emit(ctx, eventSource, eventExecutionStatusChange, d, exec.ARN)
}

// emitExecutionStarted publishes the events a freshly started STANDARD
// execution produces: RUNNING, then its terminal status when the run has
// already settled (the default synchronous model). Under AsyncSettle the
// terminal status becomes observable lazily with no clock callback to hang an
// event on, so only RUNNING is published (a StopExecution still publishes
// ABORTED).
func (m *Mock) emitExecutionStarted(ctx context.Context, exec *driver.Execution, settled bool) {
	m.emitExecutionStatus(ctx, exec, driver.ExecStatusRunning)

	if settled && exec.Status != driver.ExecStatusRunning {
		m.emitExecutionStatus(ctx, exec, exec.Status)
	}
}

func epochMillis(t time.Time) *int64 {
	if t.IsZero() {
		return nil
	}

	ms := t.UnixMilli()

	return &ms
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}

	return &s
}
