// Package bedrockagentruntime provides an in-memory mock implementation of the
// AWS Bedrock Agent runtime (bedrock-agent-runtime): InvokeAgent, Retrieve, and
// RetrieveAndGenerate. Responses are deterministic so real SDK callers get
// stable, parseable output.
package bedrockagentruntime

import (
	"context"
	"fmt"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

// Compile-time check that Mock implements driver.BedrockAgentRuntime.
var _ driver.BedrockAgentRuntime = (*Mock)(nil)

const contentTypeJSON = "application/json"

// Mock is an in-memory mock implementation of the AWS Bedrock Agent runtime.
type Mock struct {
	opts *config.Options
}

// New creates a new Bedrock Agent runtime mock.
func New(opts *config.Options) *Mock {
	return &Mock{opts: opts}
}

// InvokeAgent returns a deterministic simulated agent completion. The response
// echoes the session id from the request path; when the caller omits it a fresh
// session id is generated.
func (*Mock) InvokeAgent(_ context.Context, in driver.InvokeAgentInput) (*driver.InvokeAgentResult, error) {
	if in.AgentID == "" {
		return nil, errors.New(errors.InvalidArgument, "agentId is required")
	}

	if in.AgentAliasID == "" {
		return nil, errors.New(errors.InvalidArgument, "agentAliasId is required")
	}

	sessionID := in.SessionID
	if sessionID == "" {
		sessionID = idgen.GenerateID("session-")
	}

	var completion string
	if in.InputText == "" {
		completion = fmt.Sprintf("This is a simulated agent response from agent %s.", in.AgentID)
	} else {
		completion = fmt.Sprintf("This is a simulated agent response to: %s", in.InputText)
	}

	return &driver.InvokeAgentResult{
		Completion:  completion,
		SessionID:   sessionID,
		ContentType: contentTypeJSON,
	}, nil
}

// Retrieve returns deterministic fake chunks that echo the query text.
func (m *Mock) Retrieve(_ context.Context, in driver.RetrieveInput) (*driver.RetrieveResult, error) {
	if in.KnowledgeBaseID == "" {
		return nil, errors.New(errors.InvalidArgument, "knowledgeBaseId is required")
	}

	if in.QueryText == "" {
		return nil, errors.New(errors.InvalidArgument, "retrievalQuery.text is required")
	}

	base := fmt.Sprintf("s3://cloudemu-%s/kb/%s", m.opts.Region, in.KnowledgeBaseID)

	return &driver.RetrieveResult{
		Results: []driver.RetrievalResult{
			{
				Text:        fmt.Sprintf("Simulated knowledge-base result 1 for query: %s", in.QueryText),
				Score:       0.95,
				LocationURI: base + "/doc-1.txt",
			},
			{
				Text:        fmt.Sprintf("Simulated knowledge-base result 2 for query: %s", in.QueryText),
				Score:       0.82,
				LocationURI: base + "/doc-2.txt",
			},
		},
	}, nil
}

// RetrieveAndGenerate returns a deterministic generated answer and a session id.
// A supplied session id is reused; otherwise a new one is generated.
func (*Mock) RetrieveAndGenerate(
	_ context.Context, in driver.RetrieveAndGenerateInput,
) (*driver.RetrieveAndGenerateResult, error) {
	if in.InputText == "" {
		return nil, errors.New(errors.InvalidArgument, "input.text is required")
	}

	sessionID := in.SessionID
	if sessionID == "" {
		sessionID = idgen.GenerateID("rag-session-")
	}

	return &driver.RetrieveAndGenerateResult{
		Text:      fmt.Sprintf("This is a simulated retrieve-and-generate answer to: %s", in.InputText),
		SessionID: sessionID,
	}, nil
}
