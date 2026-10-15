package driver

import (
	"context"
	"encoding/json"
)

// Flow execution status values (FlowExecutionStatus).
const (
	FlowExecutionRunning   = "Running"
	FlowExecutionSucceeded = "Succeeded"
	FlowExecutionFailed    = "Failed"
	FlowExecutionAborted   = "Aborted"
)

// FlowInput is one input of a flow invocation: the document sent to an Input
// node output.
type FlowInput struct {
	NodeName       string
	NodeOutputName string
	NodeInputName  string
	Content        json.RawMessage
}

// FlowInvocationInput is the request for InvokeFlow and StartFlowExecution.
// Identifiers may be ids or ARNs; AliasID may be TSTALIASID (DRAFT).
type FlowInvocationInput struct {
	FlowID        string
	AliasID       string
	ExecutionID   string
	ExecutionName string
	Inputs        []FlowInput
	EnableTrace   bool
}

// FlowOutput is a document an Output node produced.
type FlowOutput struct {
	NodeName string
	NodeType string
	Content  json.RawMessage
}

// FlowTrace is a node input or output trace of a flow invocation.
type FlowTrace struct {
	Kind      string // nodeInputTrace or nodeOutputTrace
	NodeName  string
	FieldName string
	Content   json.RawMessage
	Timestamp string
}

// FlowInvocationResult is the outcome of InvokeFlow.
type FlowInvocationResult struct {
	ExecutionID      string
	Outputs          []FlowOutput
	Traces           []FlowTrace
	CompletionReason string
}

// FlowExecution is an asynchronous flow execution.
type FlowExecution struct {
	ARN         string
	FlowID      string
	AliasID     string
	FlowVersion string
	Status      string
	StartedAt   string
	EndedAt     string
}

// FlowExecutionEvent is one event of an execution: Kind is flowInputEvent,
// flowOutputEvent, nodeInputEvent or nodeOutputEvent; Fields are its named
// documents.
type FlowExecutionEvent struct {
	Kind      string
	NodeName  string
	Timestamp string
	Fields    []FlowField
}

// FlowField is a named document of a flow execution event.
type FlowField struct {
	Name    string
	Content json.RawMessage
}

// FlowSnapshot is the flow definition an execution ran.
type FlowSnapshot struct {
	FlowID                   string
	AliasID                  string
	FlowVersion              string
	Definition               string
	ExecutionRoleARN         string
	CustomerEncryptionKeyARN string
}

// FlowExecutions invokes flows and manages their asynchronous executions.
//
// Flows are invoked through their aliases. An execution can be stopped only while it is Running.
type FlowExecutions interface {
	InvokeFlow(ctx context.Context, in FlowInvocationInput) (*FlowInvocationResult, error)
	StartFlowExecution(ctx context.Context, in FlowInvocationInput) (*FlowExecution, error)
	GetFlowExecution(ctx context.Context, flowID, aliasID, executionID string) (*FlowExecution, error)
	ListFlowExecutions(ctx context.Context, flowID, aliasID string, page Page) ([]FlowExecution, string, error)
	StopFlowExecution(ctx context.Context, flowID, aliasID, executionID string) (*FlowExecution, error)
	ListFlowExecutionEvents(ctx context.Context, flowID, aliasID, executionID, eventType string, page Page) (
		[]FlowExecutionEvent, string, error)
	GetExecutionFlowSnapshot(ctx context.Context, flowID, aliasID, executionID string) (*FlowSnapshot, error)
}
