package bedrockagentruntime

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	provider "github.com/stackshy/cloudemu/v2/providers/aws/bedrockagentruntime"
	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

// primaryOnly hides every optional capability of the wrapped driver.
type primaryOnly struct{ driver.BedrockAgentRuntime }

// TestServiceEveryCapabilityMethod calls each capability method through the
// pipeline: against the provider it reaches the driver (and fails on the
// missing ids or empty input it is given), against a primary-only driver it
// fails first with FailedPrecondition.
func TestServiceEveryCapabilityMethod(t *testing.T) {
	ctx := context.Background()
	mock := provider.New(config.NewOptions())
	full := NewBedrockAgentRuntime(mock)
	bare := NewBedrockAgentRuntime(primaryOnly{mock})

	calls := map[string]func(*BedrockAgentRuntime) error{
		"CreateSession": func(svc *BedrockAgentRuntime) error {
			_, err := svc.CreateSession(ctx, driver.SessionConfig{})

			return err
		},
		"GetSession": func(svc *BedrockAgentRuntime) error {
			_, err := svc.GetSession(ctx, "MISSING000")

			return err
		},
		"UpdateSession": func(svc *BedrockAgentRuntime) error {
			_, err := svc.UpdateSession(ctx, "MISSING000", nil)

			return err
		},
		"EndSession": func(svc *BedrockAgentRuntime) error {
			_, err := svc.EndSession(ctx, "MISSING000")

			return err
		},
		"DeleteSession": func(svc *BedrockAgentRuntime) error {
			return svc.DeleteSession(ctx, "MISSING000")
		},
		"ListSessions": func(svc *BedrockAgentRuntime) error {
			_, _, err := svc.ListSessions(ctx, driver.Page{})

			return err
		},
		"CreateInvocation": func(svc *BedrockAgentRuntime) error {
			_, err := svc.CreateInvocation(ctx, "MISSING000", "MISSING000", "MISSING000")

			return err
		},
		"ListInvocations": func(svc *BedrockAgentRuntime) error {
			_, _, err := svc.ListInvocations(ctx, "MISSING000", driver.Page{})

			return err
		},
		"PutInvocationStep": func(svc *BedrockAgentRuntime) error {
			_, err := svc.PutInvocationStep(ctx, driver.InvocationStepInput{})

			return err
		},
		"GetInvocationStep": func(svc *BedrockAgentRuntime) error {
			_, err := svc.GetInvocationStep(ctx, "MISSING000", "MISSING000", "MISSING000")

			return err
		},
		"ListInvocationSteps": func(svc *BedrockAgentRuntime) error {
			_, _, err := svc.ListInvocationSteps(ctx, "MISSING000", "MISSING000", driver.Page{})

			return err
		},
		"InvokeInlineAgent": func(svc *BedrockAgentRuntime) error {
			_, err := svc.InvokeInlineAgent(ctx, driver.InlineAgentInput{})

			return err
		},
		"InvokeFlow": func(svc *BedrockAgentRuntime) error {
			_, err := svc.InvokeFlow(ctx, driver.FlowInvocationInput{})

			return err
		},
		"StartFlowExecution": func(svc *BedrockAgentRuntime) error {
			_, err := svc.StartFlowExecution(ctx, driver.FlowInvocationInput{})

			return err
		},
		"GetFlowExecution": func(svc *BedrockAgentRuntime) error {
			_, err := svc.GetFlowExecution(ctx, "MISSING000", "MISSING000", "MISSING000")

			return err
		},
		"ListFlowExecutions": func(svc *BedrockAgentRuntime) error {
			_, _, err := svc.ListFlowExecutions(ctx, "MISSING000", "MISSING000", driver.Page{})

			return err
		},
		"StopFlowExecution": func(svc *BedrockAgentRuntime) error {
			_, err := svc.StopFlowExecution(ctx, "MISSING000", "MISSING000", "MISSING000")

			return err
		},
		"ListFlowExecutionEvents": func(svc *BedrockAgentRuntime) error {
			_, _, err := svc.ListFlowExecutionEvents(ctx, "MISSING000", "MISSING000", "MISSING000", "MISSING000", driver.Page{})

			return err
		},
		"GetExecutionFlowSnapshot": func(svc *BedrockAgentRuntime) error {
			_, err := svc.GetExecutionFlowSnapshot(ctx, "MISSING000", "MISSING000", "MISSING000")

			return err
		},
		"Rerank": func(svc *BedrockAgentRuntime) error {
			_, _, err := svc.Rerank(ctx, driver.RerankInput{})

			return err
		},
		"GetAgentMemory": func(svc *BedrockAgentRuntime) error {
			_, _, err := svc.GetAgentMemory(ctx, "MISSING000", "MISSING000", "MISSING000", "MISSING000", driver.Page{})

			return err
		},
		"DeleteAgentMemory": func(svc *BedrockAgentRuntime) error {
			return svc.DeleteAgentMemory(ctx, "MISSING000", "MISSING000", "MISSING000", "MISSING000")
		},
		"TagResource": func(svc *BedrockAgentRuntime) error {
			return svc.TagResource(ctx, "MISSING000", nil)
		},
		"UntagResource": func(svc *BedrockAgentRuntime) error {
			return svc.UntagResource(ctx, "MISSING000", nil)
		},
		"ListTagsForResource": func(svc *BedrockAgentRuntime) error {
			_, err := svc.ListTagsForResource(ctx, "MISSING000")

			return err
		},
		"GenerateQuery": func(svc *BedrockAgentRuntime) error {
			_, err := svc.GenerateQuery(ctx, "MISSING000", "MISSING000")

			return err
		},
		"OptimizePrompt": func(svc *BedrockAgentRuntime) error {
			_, err := svc.OptimizePrompt(ctx, "MISSING000", "MISSING000")

			return err
		},
		"AgenticRetrieveStream": func(svc *BedrockAgentRuntime) error {
			_, err := svc.AgenticRetrieveStream(ctx, driver.AgenticRetrieveInput{})

			return err
		},
		"GetDocumentContent": func(svc *BedrockAgentRuntime) error {
			_, err := svc.GetDocumentContent(ctx, "MISSING000", "MISSING000", "MISSING000", "MISSING000")

			return err
		},
		"RetrieveAndGenerateStream": func(svc *BedrockAgentRuntime) error {
			_, err := svc.RetrieveAndGenerateStream(ctx, driver.RetrieveAndGenerateInput{})

			return err
		},
	}

	for name, call := range calls {
		if err := call(full); cerrors.IsFailedPrecondition(err) {
			t.Errorf("%s: the provider implements it, got %v", name, err)
		}

		if err := call(bare); !cerrors.IsFailedPrecondition(err) {
			t.Errorf("%s: want FailedPrecondition without the capability, got %v", name, err)
		}
	}
}
