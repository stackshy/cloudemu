package ec2

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/awsevents"
)

// EventBridge identity of EC2's native instance lifecycle event. See "Amazon
// EC2 instance state change events" in the EC2 user guide.
const (
	eventSource          = "aws.ec2"
	eventStateChangeType = "EC2 Instance State-change Notification"
)

// instanceStateChangeDetail is the detail payload of an
// "EC2 Instance State-change Notification" event.
type instanceStateChangeDetail struct {
	InstanceID string `json:"instance-id"`
	State      string `json:"state"`
}

// SetEventPublisher wires the EventBridge default bus that instance state
// transitions are published to. Safe to leave unset — no events are emitted.
func (m *Mock) SetEventPublisher(p awsevents.Publisher) {
	m.events.SetPublisher(p)
}

// emitStateChanges publishes one state-change notification per state the
// instance passed through, in order (e.g. "pending" then "running"), matching
// real EC2, which emits an event for every transition. It must be called
// outside inst.mu.
func (m *Mock) emitStateChanges(ctx context.Context, instanceID string, states ...string) {
	arn := "arn:aws:ec2:" + m.opts.Region + ":" + m.opts.AccountID + ":instance/" + instanceID

	for _, state := range states {
		m.events.Emit(ctx, eventSource, eventStateChangeType,
			instanceStateChangeDetail{InstanceID: instanceID, State: state}, arn)
	}
}
