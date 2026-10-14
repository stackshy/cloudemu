// Package driver defines the interface for the Bedrock Agent runtime
// (bedrock-agent-runtime) data-plane: invoking agents, retrieving from
// knowledge bases, and retrieve-and-generate, plus the optional capabilities in
// this package's feature files (sessions, inline agents, flow executions,
// rerank, agent memory, tags, query generation, prompt optimization, agentic
// retrieval, document content).
//
// InvokeAgent's real API streams its answer as an eventstream of chunk, trace,
// returnControl and files events. The driver returns those events in order; the
// server layer encodes them as eventstream frames.
package driver

import (
	"context"
	"encoding/json"
)

// Event kinds an agent turn produces.
const (
	EventChunk         = "chunk"
	EventTrace         = "trace"
	EventReturnControl = "returnControl"
)

// Page is the maxResults/nextToken cursor of the List operations. A nil
// MaxResults means the caller sent none.
type Page struct {
	MaxResults *int32
	NextToken  string
}

// GuardrailRef names a Bedrock guardrail version to apply.
type GuardrailRef struct {
	ID      string
	Version string
}

// InvocationResult is one action-group result the caller sends back after a
// returnControl event (sessionState.returnControlInvocationResults).
type InvocationResult struct {
	ActionGroup       string
	Function          string
	APIPath           string
	HTTPMethod        string
	HTTPStatusCode    int32
	ResponseBody      string
	ResponseState     string
	ConfirmationState string
}

// SessionState is the sessionState of an agent invocation.
type SessionState struct {
	SessionAttributes       map[string]string
	PromptSessionAttributes map[string]string
	InvocationID            string
	ReturnControlResults    []InvocationResult
	KnowledgeBases          []KnowledgeBaseConfig // per-call retrieval settings of the agent's knowledge bases
}

// KnowledgeBaseConfig overrides how one of an agent's knowledge bases is
// searched for a call: the number of results and a RetrievalFilter.
type KnowledgeBaseConfig struct {
	KnowledgeBaseID string
	NumberOfResults *int32
	Filter          json.RawMessage
}

// InvokeAgentInput is the request for the InvokeAgent operation. AgentID,
// AgentAliasID, and SessionID are path parameters; InputText is the prompt.
type InvokeAgentInput struct {
	AgentID      string
	AgentAliasID string
	SessionID    string
	InputText    string
	EnableTrace  bool
	EndSession   bool
	MemoryID     string
	SessionState *SessionState
}

// Parameter is a named, typed value passed to or from an action group.
type Parameter struct {
	Name  string
	Type  string
	Value string
}

// InvocationInput is one action the agent asks the caller to run (a function
// of a function-schema action group, or an API of an OpenAPI one).
type InvocationInput struct {
	ActionGroup string
	Function    string
	APIPath     string
	HTTPMethod  string
	Parameters  []Parameter
}

// ReturnControl hands an action-group call back to the caller.
type ReturnControl struct {
	InvocationID string
	Inputs       []InvocationInput
}

// Trace is one trace event of an agent turn. Kind is the Trace union member
// (preProcessingTrace, orchestrationTrace, postProcessingTrace, guardrailTrace)
// and Part the member inside it (modelInvocationInput, modelInvocationOutput,
// rationale, invocationInput, observation).
type Trace struct {
	AgentID         string
	AgentAliasID    string
	AgentVersion    string
	SessionID       string
	EventTime       string
	Kind            string
	Part            string
	TraceID         string
	PromptType      string
	FoundationModel string
	Text            string
	ObservationType string
	ActionGroup     string
	Function        string
	GuardrailAction string
	InputTokens     int32
	OutputTokens    int32
	References      []RetrievalResult // a knowledge-base observation's retrieved chunks
}

// Citation ties a span of generated text to the retrieved chunks it used.
type Citation struct {
	Text       string
	Start      int32
	End        int32
	References []RetrievalResult
}

// Chunk is a piece of the agent's answer and the citations for it.
type Chunk struct {
	Text      string
	Citations []Citation
}

// AgentEvent is one event of an agent turn: exactly one of Chunk, Trace and
// ReturnControl is set.
type AgentEvent struct {
	Chunk         *Chunk
	Trace         *Trace
	ReturnControl *ReturnControl
}

// InvokeAgentResult carries the assembled agent completion and the events of
// the turn, in order. ContentType is the media type of the completion payload.
type InvokeAgentResult struct {
	Completion  string
	SessionID   string
	ContentType string
	MemoryID    string
	Events      []AgentEvent
}

// RetrieveInput is the request for the Retrieve operation. KnowledgeBaseID is a
// path parameter (id or ARN); QueryText is the retrieval query text (required).
type RetrieveInput struct {
	KnowledgeBaseID string
	QueryText       string
	NextToken       string
	NumberOfResults *int32
	Guardrail       *GuardrailRef
	Filter          json.RawMessage // vectorSearchConfiguration.filter, a RetrievalFilter
}

// Retrieval-result location types.
const (
	LocationS3     = "S3"
	LocationCustom = "CUSTOM"
)

// RetrievalResult is a single chunk returned from a knowledge-base query.
// LocationType is S3 (LocationURI) or CUSTOM (CustomDocumentID).
type RetrievalResult struct {
	Text             string
	Score            float64
	LocationType     string
	LocationURI      string
	CustomDocumentID string
	DocumentID       string
	DataSourceID     string
	Metadata         map[string]any // the chunk's metadata attributes, keyed by name
}

// RetrieveResult is the response from the Retrieve operation.
type RetrieveResult struct {
	Results         []RetrievalResult
	NextToken       string
	GuardrailAction string
}

// RetrieveAndGenerate source types.
const (
	RAGKnowledgeBase   = "KNOWLEDGE_BASE"
	RAGExternalSources = "EXTERNAL_SOURCES"
)

// ExternalSource is a document an EXTERNAL_SOURCES request carries: inline
// bytes (Text) or an S3 URI.
type ExternalSource struct {
	SourceType string
	S3URI      string
	Name       string
	Text       string
}

// RetrieveAndGenerateInput is the request for the RetrieveAndGenerate
// operation. InputText is the query (required); SessionID is optional and
// continues an existing session when supplied. A nil Type keeps the legacy
// behavior of answering without a source.
type RetrieveAndGenerateInput struct {
	InputText       string
	SessionID       string
	Type            string
	KnowledgeBaseID string
	ModelARN        string
	NumberOfResults *int32
	ExternalSources []ExternalSource
	Guardrail       *GuardrailRef
	Filter          json.RawMessage // the knowledge base's retrieval filter
}

// RetrieveAndGenerateResult is the response from the RetrieveAndGenerate
// operation.
type RetrieveAndGenerateResult struct {
	Text            string
	SessionID       string
	Citations       []Citation
	GuardrailAction string
}

// BedrockAgentRuntime is the interface that Bedrock Agent runtime
// implementations must satisfy.
type BedrockAgentRuntime interface {
	InvokeAgent(ctx context.Context, in InvokeAgentInput) (*InvokeAgentResult, error)
	Retrieve(ctx context.Context, in RetrieveInput) (*RetrieveResult, error)
	RetrieveAndGenerate(ctx context.Context, in RetrieveAndGenerateInput) (*RetrieveAndGenerateResult, error)
}
