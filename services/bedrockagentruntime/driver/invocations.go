package driver

import (
	"context"
	"encoding/json"
)

// Invocation is a group of steps recorded in a session.
type Invocation struct {
	ID          string
	SessionID   string
	Description string
	CreatedAt   string
}

// InvocationStepInput describes a step to record. Payload is the
// InvocationStepPayload union (contentBlocks of text and images), kept
// verbatim. An empty StepID mints one.
type InvocationStepInput struct {
	SessionID    string
	InvocationID string
	StepID       string
	StepTime     string
	Payload      json.RawMessage
}

// InvocationStep is a recorded step of an invocation.
type InvocationStep struct {
	ID           string
	SessionID    string
	InvocationID string
	StepTime     string
	Payload      json.RawMessage
}

// Invocations records invocations and their steps in a session.
//
// An ENDED session takes no new invocations or steps.
type Invocations interface {
	CreateInvocation(ctx context.Context, sessionID, invocationID, description string) (*Invocation, error)
	ListInvocations(ctx context.Context, sessionID string, page Page) ([]Invocation, string, error)
	PutInvocationStep(ctx context.Context, in InvocationStepInput) (string, error)
	GetInvocationStep(ctx context.Context, sessionID, invocationID, stepID string) (*InvocationStep, error)
	ListInvocationSteps(ctx context.Context, sessionID, invocationID string, page Page) ([]InvocationStep, string, error)
}
