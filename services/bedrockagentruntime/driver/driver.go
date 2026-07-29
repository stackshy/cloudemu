// Package driver defines the interface for the Bedrock Agent runtime
// (bedrock-agent-runtime) data-plane: invoking agents, retrieving from
// knowledge bases, and retrieve-and-generate.
//
// InvokeAgent's real API streams its answer as an eventstream of chunk events.
// The driver models the response as the fully-assembled completion text plus a
// session id; the server layer is responsible for splitting that text into
// eventstream chunk frames on the wire.
package driver

import "context"

// InvokeAgentInput is the request for the InvokeAgent operation. AgentID,
// AgentAliasID, and SessionID are path parameters; InputText is the prompt.
type InvokeAgentInput struct {
	AgentID      string
	AgentAliasID string
	SessionID    string
	InputText    string
	EnableTrace  bool
	EndSession   bool
}

// InvokeAgentResult carries the assembled agent completion. ContentType is the
// media type of the completion payload (application/json for text answers).
type InvokeAgentResult struct {
	Completion  string
	SessionID   string
	ContentType string
}

// RetrieveInput is the request for the Retrieve operation. KnowledgeBaseID is a
// path parameter; QueryText is the retrieval query text (required).
type RetrieveInput struct {
	KnowledgeBaseID string
	QueryText       string
	NextToken       string
}

// RetrievalResult is a single chunk returned from a knowledge-base query.
type RetrievalResult struct {
	Text        string
	Score       float64
	LocationURI string
}

// RetrieveResult is the response from the Retrieve operation.
type RetrieveResult struct {
	Results   []RetrievalResult
	NextToken string
}

// RetrieveAndGenerateInput is the request for the RetrieveAndGenerate
// operation. InputText is the query (required); SessionID is optional and
// continues an existing session when supplied.
type RetrieveAndGenerateInput struct {
	InputText string
	SessionID string
}

// RetrieveAndGenerateResult is the response from the RetrieveAndGenerate
// operation.
type RetrieveAndGenerateResult struct {
	Text      string
	SessionID string
}

// BedrockAgentRuntime is the interface that Bedrock Agent runtime
// implementations must satisfy.
type BedrockAgentRuntime interface {
	InvokeAgent(ctx context.Context, in InvokeAgentInput) (*InvokeAgentResult, error)
	Retrieve(ctx context.Context, in RetrieveInput) (*RetrieveResult, error)
	RetrieveAndGenerate(ctx context.Context, in RetrieveAndGenerateInput) (*RetrieveAndGenerateResult, error)
}
