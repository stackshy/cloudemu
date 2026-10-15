package bedrockagentruntime

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

// The methods in this file expose the optional driver capabilities (sessions,
// invocations, inline agents, flow executions, rerank, agent memory, tags,
// query generation, prompt optimization, agentic retrieval, document content,
// streamed retrieve-and-generate) through the same error-injection,
// rate-limit, latency, metrics and recording pipeline as the primary
// operations. A driver that does not implement a capability answers
// FailedPrecondition.

// capabilityOf returns the optional capability T of the wrapped driver.
func capabilityOf[T any](b *BedrockAgentRuntime, op string) (T, error) {
	c, ok := b.driver.(T)
	if !ok {
		var zero T

		return zero, cerrors.Newf(cerrors.FailedPrecondition, "the Bedrock Agent runtime driver does not implement %s", op)
	}

	return c, nil
}

// call runs fn through the pipeline and returns its typed result.
func call[T any](ctx context.Context, b *BedrockAgentRuntime, op string, input any, fn func() (T, error)) (T, error) {
	out, err := b.do(ctx, op, input, func() (any, error) { return fn() })
	if err != nil {
		var zero T

		return zero, err
	}

	return out.(T), nil
}

// callErr runs an operation that returns only an error through the pipeline.
func callErr(ctx context.Context, b *BedrockAgentRuntime, op string, input any, fn func() error) error {
	_, err := b.do(ctx, op, input, func() (any, error) { return nil, fn() })

	return err
}

// page is one page of a List result.
type page[T any] struct {
	items []T
	next  string
}

// listCall runs a paged operation through the pipeline.
func listCall[T any](ctx context.Context, b *BedrockAgentRuntime, op string, input any,
	fn func() ([]T, string, error),
) (items []T, nextToken string, err error) {
	out, err := b.do(ctx, op, input, func() (any, error) {
		items, next, ferr := fn()
		if ferr != nil {
			return nil, ferr
		}

		return page[T]{items: items, next: next}, nil
	})
	if err != nil {
		return nil, "", err
	}

	p := out.(page[T])

	return p.items, p.next, nil
}

// CreateSession starts a session.
func (b *BedrockAgentRuntime) CreateSession(ctx context.Context, cfg driver.SessionConfig) (*driver.Session, error) {
	c, err := capabilityOf[driver.Sessions](b, "CreateSession")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "CreateSession", cfg, func() (*driver.Session, error) {
		return c.CreateSession(ctx, cfg)
	})
}

// GetSession returns a session.
func (b *BedrockAgentRuntime) GetSession(ctx context.Context, sessionID string) (*driver.Session, error) {
	c, err := capabilityOf[driver.Sessions](b, "GetSession")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "GetSession", sessionID, func() (*driver.Session, error) {
		return c.GetSession(ctx, sessionID)
	})
}

// UpdateSession replaces a session's metadata.
func (b *BedrockAgentRuntime) UpdateSession(
	ctx context.Context, sessionID string, metadata map[string]string,
) (*driver.Session, error) {
	c, err := capabilityOf[driver.Sessions](b, "UpdateSession")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "UpdateSession", sessionID, func() (*driver.Session, error) {
		return c.UpdateSession(ctx, sessionID, metadata)
	})
}

// EndSession ends a session.
func (b *BedrockAgentRuntime) EndSession(ctx context.Context, sessionID string) (*driver.Session, error) {
	c, err := capabilityOf[driver.Sessions](b, "EndSession")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "EndSession", sessionID, func() (*driver.Session, error) {
		return c.EndSession(ctx, sessionID)
	})
}

// DeleteSession deletes an ended session.
func (b *BedrockAgentRuntime) DeleteSession(ctx context.Context, sessionID string) error {
	c, err := capabilityOf[driver.Sessions](b, "DeleteSession")
	if err != nil {
		return err
	}

	return callErr(ctx, b, "DeleteSession", sessionID, func() error {
		return c.DeleteSession(ctx, sessionID)
	})
}

// ListSessions lists one page of sessions.
func (b *BedrockAgentRuntime) ListSessions(ctx context.Context, page driver.Page) ([]driver.Session, string, error) {
	c, err := capabilityOf[driver.Sessions](b, "ListSessions")
	if err != nil {
		return nil, "", err
	}

	return listCall(ctx, b, "ListSessions", page, func() ([]driver.Session, string, error) {
		return c.ListSessions(ctx, page)
	})
}

// CreateInvocation adds an invocation to a session.
func (b *BedrockAgentRuntime) CreateInvocation(
	ctx context.Context, sessionID, invocationID, description string,
) (*driver.Invocation, error) {
	c, err := capabilityOf[driver.Invocations](b, "CreateInvocation")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "CreateInvocation", sessionID, func() (*driver.Invocation, error) {
		return c.CreateInvocation(ctx, sessionID, invocationID, description)
	})
}

// ListInvocations lists one page of a session's invocations.
func (b *BedrockAgentRuntime) ListInvocations(
	ctx context.Context, sessionID string, page driver.Page,
) ([]driver.Invocation, string, error) {
	c, err := capabilityOf[driver.Invocations](b, "ListInvocations")
	if err != nil {
		return nil, "", err
	}

	return listCall(ctx, b, "ListInvocations", sessionID, func() ([]driver.Invocation, string, error) {
		return c.ListInvocations(ctx, sessionID, page)
	})
}

// PutInvocationStep records an invocation step.
//
//nolint:gocritic // the argument matches the driver interface signature; copied once on entry.
func (b *BedrockAgentRuntime) PutInvocationStep(ctx context.Context, in driver.InvocationStepInput) (string, error) {
	c, err := capabilityOf[driver.Invocations](b, "PutInvocationStep")
	if err != nil {
		return "", err
	}

	return call(ctx, b, "PutInvocationStep", in, func() (string, error) {
		return c.PutInvocationStep(ctx, in)
	})
}

// GetInvocationStep returns an invocation step.
func (b *BedrockAgentRuntime) GetInvocationStep(
	ctx context.Context, sessionID, invocationID, stepID string,
) (*driver.InvocationStep, error) {
	c, err := capabilityOf[driver.Invocations](b, "GetInvocationStep")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "GetInvocationStep", stepID, func() (*driver.InvocationStep, error) {
		return c.GetInvocationStep(ctx, sessionID, invocationID, stepID)
	})
}

// ListInvocationSteps lists one page of invocation steps.
func (b *BedrockAgentRuntime) ListInvocationSteps(
	ctx context.Context, sessionID, invocationID string, page driver.Page,
) ([]driver.InvocationStep, string, error) {
	c, err := capabilityOf[driver.Invocations](b, "ListInvocationSteps")
	if err != nil {
		return nil, "", err
	}

	return listCall(ctx, b, "ListInvocationSteps", sessionID, func() ([]driver.InvocationStep, string, error) {
		return c.ListInvocationSteps(ctx, sessionID, invocationID, page)
	})
}

// InvokeInlineAgent runs an agent defined in the request.
//
//nolint:gocritic // the argument matches the driver interface signature; copied once on entry.
func (b *BedrockAgentRuntime) InvokeInlineAgent(
	ctx context.Context, in driver.InlineAgentInput,
) (*driver.InvokeAgentResult, error) {
	c, err := capabilityOf[driver.InlineAgents](b, "InvokeInlineAgent")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "InvokeInlineAgent", in, func() (*driver.InvokeAgentResult, error) {
		return c.InvokeInlineAgent(ctx, in)
	})
}

// InvokeFlow runs a flow through an alias.
//
//nolint:gocritic // the argument matches the driver interface signature; copied once on entry.
func (b *BedrockAgentRuntime) InvokeFlow(
	ctx context.Context, in driver.FlowInvocationInput,
) (*driver.FlowInvocationResult, error) {
	c, err := capabilityOf[driver.FlowExecutions](b, "InvokeFlow")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "InvokeFlow", in, func() (*driver.FlowInvocationResult, error) {
		return c.InvokeFlow(ctx, in)
	})
}

// StartFlowExecution starts an asynchronous flow execution.
//
//nolint:gocritic // the argument matches the driver interface signature; copied once on entry.
func (b *BedrockAgentRuntime) StartFlowExecution(
	ctx context.Context, in driver.FlowInvocationInput,
) (*driver.FlowExecution, error) {
	c, err := capabilityOf[driver.FlowExecutions](b, "StartFlowExecution")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "StartFlowExecution", in, func() (*driver.FlowExecution, error) {
		return c.StartFlowExecution(ctx, in)
	})
}

// GetFlowExecution returns a flow execution.
func (b *BedrockAgentRuntime) GetFlowExecution(
	ctx context.Context, flowID, aliasID, executionID string,
) (*driver.FlowExecution, error) {
	c, err := capabilityOf[driver.FlowExecutions](b, "GetFlowExecution")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "GetFlowExecution", executionID, func() (*driver.FlowExecution, error) {
		return c.GetFlowExecution(ctx, flowID, aliasID, executionID)
	})
}

// ListFlowExecutions lists one page of a flow's executions.
func (b *BedrockAgentRuntime) ListFlowExecutions(
	ctx context.Context, flowID, aliasID string, page driver.Page,
) ([]driver.FlowExecution, string, error) {
	c, err := capabilityOf[driver.FlowExecutions](b, "ListFlowExecutions")
	if err != nil {
		return nil, "", err
	}

	return listCall(ctx, b, "ListFlowExecutions", flowID, func() ([]driver.FlowExecution, string, error) {
		return c.ListFlowExecutions(ctx, flowID, aliasID, page)
	})
}

// StopFlowExecution stops a running flow execution.
func (b *BedrockAgentRuntime) StopFlowExecution(
	ctx context.Context, flowID, aliasID, executionID string,
) (*driver.FlowExecution, error) {
	c, err := capabilityOf[driver.FlowExecutions](b, "StopFlowExecution")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "StopFlowExecution", executionID, func() (*driver.FlowExecution, error) {
		return c.StopFlowExecution(ctx, flowID, aliasID, executionID)
	})
}

// ListFlowExecutionEvents lists one page of a flow execution's events.
func (b *BedrockAgentRuntime) ListFlowExecutionEvents(
	ctx context.Context, flowID, aliasID, executionID, eventType string, page driver.Page,
) ([]driver.FlowExecutionEvent, string, error) {
	c, err := capabilityOf[driver.FlowExecutions](b, "ListFlowExecutionEvents")
	if err != nil {
		return nil, "", err
	}

	return listCall(ctx, b, "ListFlowExecutionEvents", executionID, func() ([]driver.FlowExecutionEvent, string, error) {
		return c.ListFlowExecutionEvents(ctx, flowID, aliasID, executionID, eventType, page)
	})
}

// GetExecutionFlowSnapshot returns the definition a flow execution ran.
func (b *BedrockAgentRuntime) GetExecutionFlowSnapshot(
	ctx context.Context, flowID, aliasID, executionID string,
) (*driver.FlowSnapshot, error) {
	c, err := capabilityOf[driver.FlowExecutions](b, "GetExecutionFlowSnapshot")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "GetExecutionFlowSnapshot", executionID, func() (*driver.FlowSnapshot, error) {
		return c.GetExecutionFlowSnapshot(ctx, flowID, aliasID, executionID)
	})
}

// Rerank orders documents by relevance to a query.
//
//nolint:gocritic // the argument matches the driver interface signature; copied once on entry.
func (b *BedrockAgentRuntime) Rerank(ctx context.Context, in driver.RerankInput) ([]driver.RerankResult, string, error) {
	c, err := capabilityOf[driver.Reranker](b, "Rerank")
	if err != nil {
		return nil, "", err
	}

	return listCall(ctx, b, "Rerank", in, func() ([]driver.RerankResult, string, error) {
		return c.Rerank(ctx, in)
	})
}

// GetAgentMemory lists the session summaries kept under a memory id.
func (b *BedrockAgentRuntime) GetAgentMemory(
	ctx context.Context, agentID, aliasID, memoryID, memoryType string, page driver.Page,
) ([]driver.MemorySummary, string, error) {
	c, err := capabilityOf[driver.AgentMemory](b, "GetAgentMemory")
	if err != nil {
		return nil, "", err
	}

	return listCall(ctx, b, "GetAgentMemory", memoryID, func() ([]driver.MemorySummary, string, error) {
		return c.GetAgentMemory(ctx, agentID, aliasID, memoryID, memoryType, page)
	})
}

// DeleteAgentMemory deletes agent memory.
func (b *BedrockAgentRuntime) DeleteAgentMemory(ctx context.Context, agentID, aliasID, memoryID, sessionID string) error {
	c, err := capabilityOf[driver.AgentMemory](b, "DeleteAgentMemory")
	if err != nil {
		return err
	}

	return callErr(ctx, b, "DeleteAgentMemory", memoryID, func() error {
		return c.DeleteAgentMemory(ctx, agentID, aliasID, memoryID, sessionID)
	})
}

// TagResource tags a session.
func (b *BedrockAgentRuntime) TagResource(ctx context.Context, resourceARN string, tags map[string]string) error {
	c, err := capabilityOf[driver.Tagger](b, "TagResource")
	if err != nil {
		return err
	}

	return callErr(ctx, b, "TagResource", resourceARN, func() error {
		return c.TagResource(ctx, resourceARN, tags)
	})
}

// UntagResource removes tags from a session.
func (b *BedrockAgentRuntime) UntagResource(ctx context.Context, resourceARN string, tagKeys []string) error {
	c, err := capabilityOf[driver.Tagger](b, "UntagResource")
	if err != nil {
		return err
	}

	return callErr(ctx, b, "UntagResource", resourceARN, func() error {
		return c.UntagResource(ctx, resourceARN, tagKeys)
	})
}

// ListTagsForResource returns a session's tags.
func (b *BedrockAgentRuntime) ListTagsForResource(ctx context.Context, resourceARN string) (map[string]string, error) {
	c, err := capabilityOf[driver.Tagger](b, "ListTagsForResource")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "ListTagsForResource", resourceARN, func() (map[string]string, error) {
		return c.ListTagsForResource(ctx, resourceARN)
	})
}

// GenerateQuery turns a question into a query.
func (b *BedrockAgentRuntime) GenerateQuery(ctx context.Context, text, knowledgeBaseARN string) ([]driver.GeneratedQuery, error) {
	c, err := capabilityOf[driver.QueryGenerator](b, "GenerateQuery")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "GenerateQuery", knowledgeBaseARN, func() ([]driver.GeneratedQuery, error) {
		return c.GenerateQuery(ctx, text, knowledgeBaseARN)
	})
}

// OptimizePrompt rewrites a prompt for a model.
func (b *BedrockAgentRuntime) OptimizePrompt(ctx context.Context, text, targetModelID string) (*driver.OptimizedPrompt, error) {
	c, err := capabilityOf[driver.PromptOptimizer](b, "OptimizePrompt")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "OptimizePrompt", targetModelID, func() (*driver.OptimizedPrompt, error) {
		return c.OptimizePrompt(ctx, text, targetModelID)
	})
}

// AgenticRetrieveStream plans and runs retrievals over knowledge bases.
func (b *BedrockAgentRuntime) AgenticRetrieveStream(
	ctx context.Context, in driver.AgenticRetrieveInput,
) (*driver.AgenticRetrieveResult, error) {
	c, err := capabilityOf[driver.AgenticRetriever](b, "AgenticRetrieveStream")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "AgenticRetrieveStream", in, func() (*driver.AgenticRetrieveResult, error) {
		return c.AgenticRetrieveStream(ctx, in)
	})
}

// GetDocumentContent locates a knowledge-base document's content.
func (b *BedrockAgentRuntime) GetDocumentContent(
	ctx context.Context, kbID, dsID, documentID, format string,
) (*driver.DocumentContent, error) {
	c, err := capabilityOf[driver.DocumentReader](b, "GetDocumentContent")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "GetDocumentContent", documentID, func() (*driver.DocumentContent, error) {
		return c.GetDocumentContent(ctx, kbID, dsID, documentID, format)
	})
}

// RetrieveAndGenerateStream answers from retrieved sources, streamed.
//
//nolint:gocritic // the argument matches the driver interface signature; copied once on entry.
func (b *BedrockAgentRuntime) RetrieveAndGenerateStream(
	ctx context.Context, in driver.RetrieveAndGenerateInput,
) (*driver.RetrieveAndGenerateResult, error) {
	c, err := capabilityOf[driver.StreamingRetrieveAndGenerate](b, "RetrieveAndGenerateStream")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "RetrieveAndGenerateStream", in, func() (*driver.RetrieveAndGenerateResult, error) {
		return c.RetrieveAndGenerateStream(ctx, in)
	})
}
