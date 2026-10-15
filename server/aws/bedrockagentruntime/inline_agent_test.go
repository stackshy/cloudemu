package bedrockagentruntime_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsba "github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	agentruntime "github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
	rttypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSDKInvokeInlineAgent(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	run := func(in *agentruntime.InvokeInlineAgentInput) (text string, rc *rttypes.InlineAgentReturnControlPayload) {
		out, err := s.runtime.InvokeInlineAgent(ctx, in)
		require.NoError(t, err)

		stream := out.GetStream()
		defer stream.Close()

		for ev := range stream.Events() {
			switch e := ev.(type) {
			case *rttypes.InlineAgentResponseStreamMemberChunk:
				text += string(e.Value.Bytes)
			case *rttypes.InlineAgentResponseStreamMemberReturnControl:
				v := e.Value
				rc = &v
			}
		}

		requireStreamDone(t, stream.Err())

		return text, rc
	}

	text, _ := run(&agentruntime.InvokeInlineAgentInput{
		SessionId: aws.String("inline-1"), FoundationModel: aws.String(claudeModel), Instruction: aws.String(instruction),
		InputText: aws.String("hello inline"),
	})
	assert.Contains(t, text, "hello inline")

	_, rc := run(&agentruntime.InvokeInlineAgentInput{
		SessionId: aws.String("inline-2"), FoundationModel: aws.String(claudeModel), Instruction: aws.String(instruction),
		InputText: aws.String("please cancel_order now"),
		ActionGroups: []rttypes.AgentActionGroup{{
			ActionGroupName:     aws.String("orders"),
			ActionGroupExecutor: &rttypes.ActionGroupExecutorMemberCustomControl{Value: rttypes.CustomControlMethodReturnControl},
			FunctionSchema: &rttypes.FunctionSchemaMemberFunctions{Value: []rttypes.FunctionDefinition{{
				Name: aws.String("cancel_order"),
			}}},
		}},
	})
	require.NotNil(t, rc)

	_, err := s.runtime.InvokeInlineAgent(ctx, &agentruntime.InvokeInlineAgentInput{
		SessionId: aws.String("inline-3"), FoundationModel: aws.String(claudeModel), Instruction: aws.String("too short"),
	})
	requireAPIError(t, err, "ValidationException", http.StatusBadRequest)

	// PrepareAgent (POST /agents/{id}/) still reaches the control plane.
	agentID := s.newAgent(t, "prep", nil)
	prepared, err := s.agent.PrepareAgent(ctx, &awsba.PrepareAgentInput{AgentId: aws.String(agentID)})
	require.NoError(t, err)
	assert.Equal(t, "PREPARED", string(prepared.AgentStatus))
}

// TestSDKInlineAgentRequestShapes sends an inline agent the request members
// the other tests leave out: a Lambda action group with typed function
// parameters, a knowledge base with a result count, a guardrail, and
// inlineSessionState with an API-type returnControl result and history.
func TestSDKInlineAgentRequestShapes(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	kbID, _ := s.newKnowledgeBase(t, "faq", map[string]string{"refunds": "Refunds take 5 days."})

	guardrailID := s.newGuardrail(t)

	out, err := s.runtime.InvokeInlineAgent(ctx, &agentruntime.InvokeInlineAgentInput{
		SessionId: aws.String("inline-shapes"), FoundationModel: aws.String(claudeModel), Instruction: aws.String(instruction),
		InputText: aws.String("refunds"),
		ActionGroups: []rttypes.AgentActionGroup{{
			ActionGroupName:     aws.String("tools"),
			ActionGroupExecutor: &rttypes.ActionGroupExecutorMemberLambda{Value: "arn:aws:lambda:us-east-1:123456789012:function:none"},
			FunctionSchema: &rttypes.FunctionSchemaMemberFunctions{Value: []rttypes.FunctionDefinition{{
				Name:       aws.String("lookup_order"),
				Parameters: map[string]rttypes.ParameterDetail{"order_id": {Type: rttypes.ParameterTypeString, Required: aws.Bool(true)}},
			}}},
		}},
		KnowledgeBases: []rttypes.KnowledgeBase{{
			KnowledgeBaseId: aws.String(kbID), Description: aws.String("faq"),
			RetrievalConfiguration: &rttypes.KnowledgeBaseRetrievalConfiguration{
				VectorSearchConfiguration: &rttypes.KnowledgeBaseVectorSearchConfiguration{NumberOfResults: aws.Int32(1)},
			},
		}},
		GuardrailConfiguration: &rttypes.GuardrailConfigurationWithArn{
			GuardrailIdentifier: aws.String(guardrailID), GuardrailVersion: aws.String("DRAFT"),
		},
		InlineSessionState: &rttypes.InlineSessionState{
			ConversationHistory: &rttypes.ConversationHistory{Messages: []rttypes.Message{
				{Role: rttypes.ConversationRoleUser, Content: []rttypes.ContentBlock{&rttypes.ContentBlockMemberText{Value: "hi"}}},
			}},
		},
	})
	require.NoError(t, err)

	text := ""
	stream := out.GetStream()

	for ev := range stream.Events() {
		if c, ok := ev.(*rttypes.InlineAgentResponseStreamMemberChunk); ok {
			text += string(c.Value.Bytes)
		}
	}

	requireStreamDone(t, stream.Err())
	_ = stream.Close()
	assert.Contains(t, text, "Refunds take 5 days.")

	_, err = s.runtime.InvokeInlineAgent(ctx, &agentruntime.InvokeInlineAgentInput{
		SessionId: aws.String("inline-shapes"), FoundationModel: aws.String(claudeModel), Instruction: aws.String(instruction),
		InlineSessionState: &rttypes.InlineSessionState{
			InvocationId: aws.String("00000000-0000-4000-8000-000000000000"),
			ReturnControlInvocationResults: []rttypes.InvocationResultMember{&rttypes.InvocationResultMemberMemberApiResult{
				Value: rttypes.ApiResult{ActionGroup: aws.String("tools"), ApiPath: aws.String("/orders"), HttpMethod: aws.String("GET"),
					ResponseBody: map[string]rttypes.ContentBody{"application/json": {Body: aws.String(`{"ok":true}`)}}},
			}},
		},
	})
	requireAPIError(t, err, "ValidationException", http.StatusBadRequest)

	status, errType := s.raw(t, http.MethodPatch, "/tags/arn:aws:bedrock:us-east-1:123456789012:session/00000000-0000-4000-8000-000000000000", "")
	assert.Equal(t, http.StatusMethodNotAllowed, status)
	assert.Equal(t, "ValidationException", errType)
}
