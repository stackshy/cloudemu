package ssm

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/awsevents"
)

// EventBridge identity of Parameter Store's native change event. See
// "Monitoring Systems Manager events with Amazon EventBridge" (Parameter Store
// change events) in the Systems Manager user guide.
const (
	eventSource             = "aws.ssm"
	eventParameterChange    = "Parameter Store Change"
	opCreate                = "Create"
	opUpdate                = "Update"
	opDelete                = "Delete"
	opLabelParameterVersion = "LabelParameterVersion"
)

// parameterChangeDetail is the detail payload of a "Parameter Store Change"
// event.
type parameterChangeDetail struct {
	Operation   string `json:"operation"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// SetEventPublisher wires the EventBridge default bus that parameter changes
// are published to. Safe to leave unset. No events are emitted.
func (m *Mock) SetEventPublisher(p awsevents.Publisher) {
	m.events.SetPublisher(p)
}

// typeAndDescription reads the parameter's current (latest-version) type and
// its description under the parameter's read lock.
func (pd *paramData) typeAndDescription() (typ, description string) {
	pd.mu.RLock()
	defer pd.mu.RUnlock()

	if v, ok := pd.versionByNumber(pd.latest); ok {
		typ = v.typ
	}

	return typ, pd.description
}

// emitParameterChange publishes a change event for a parameter that still
// exists, reading its type and description from the store.
func (m *Mock) emitParameterChange(ctx context.Context, operation, name string) {
	pd, ok := m.params.Get(name)
	if !ok {
		return
	}

	typ, description := pd.typeAndDescription()
	m.emitParameterChangeOf(ctx, operation, name, typ, description)
}

// emitParameterChangeOf publishes a "Parameter Store Change" event. It must be
// called without the parameter's lock held.
func (m *Mock) emitParameterChangeOf(ctx context.Context, operation, name, typ, description string) {
	m.events.Emit(ctx, eventSource, eventParameterChange, parameterChangeDetail{
		Operation: operation, Name: name, Type: typ, Description: description,
	}, m.arn(name))
}
