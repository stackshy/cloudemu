package driver

import (
	"context"
	"encoding/json"
)

// InlineActionGroup is an action group defined inline in the request. Its
// executor is either return control (ReturnControl) or a Lambda function
// (LambdaARN); Functions is its function schema.
type InlineActionGroup struct {
	Name                       string
	Description                string
	ParentActionGroupSignature string
	ReturnControl              bool
	LambdaARN                  string
	Functions                  []FunctionDef
	APISchema                  json.RawMessage
}

// FunctionDef is one function of a function-schema action group.
type FunctionDef struct {
	Name        string
	Description string
	Parameters  []ParameterDef
}

// ParameterDef is a declared function parameter.
type ParameterDef struct {
	Name     string
	Type     string
	Required bool
}

// InlineKnowledgeBase is a knowledge base attached to an inline agent.
type InlineKnowledgeBase struct {
	KnowledgeBaseID string
	Description     string
	NumberOfResults *int32
	Filter          json.RawMessage
}

// InlineAgentInput is the request for InvokeInlineAgent: the whole agent is
// defined in the request instead of being created first.
type InlineAgentInput struct {
	SessionID               string
	AgentName               string
	FoundationModel         string
	Instruction             string
	InputText               string
	EnableTrace             bool
	EndSession              bool
	IdleSessionTTLInSeconds int32
	AgentCollaboration      string
	ActionGroups            []InlineActionGroup
	KnowledgeBases          []InlineKnowledgeBase
	Guardrail               *GuardrailRef
	SessionState            *SessionState
}

// InlineAgents runs agents defined inline in the request.
type InlineAgents interface {
	InvokeInlineAgent(ctx context.Context, in InlineAgentInput) (*InvokeAgentResult, error)
}
