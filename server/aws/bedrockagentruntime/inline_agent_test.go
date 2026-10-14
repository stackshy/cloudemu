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
