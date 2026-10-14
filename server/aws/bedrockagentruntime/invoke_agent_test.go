package bedrockagentruntime_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsba "github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	batypes "github.com/aws/aws-sdk-go-v2/service/bedrockagent/types"
	agentruntime "github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
	rttypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	serverlessdriver "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// agentTurn collects the events of one InvokeAgent call.
type agentTurn struct {
	text          string
	traces        []rttypes.TracePart
	returnControl *rttypes.ReturnControlPayload
	citations     int
	sessionID     string
}

func invoke(t *testing.T, s *stack, in *agentruntime.InvokeAgentInput) (*agentTurn, error) {
	t.Helper()

	out, err := s.runtime.InvokeAgent(context.Background(), in)
	if err != nil {
		return nil, err
	}

	stream := out.GetStream()
	defer stream.Close()

	turn := &agentTurn{sessionID: aws.ToString(out.SessionId)}

	for ev := range stream.Events() {
		switch e := ev.(type) {
		case *rttypes.ResponseStreamMemberChunk:
			turn.text += string(e.Value.Bytes)
			if e.Value.Attribution != nil {
				turn.citations += len(e.Value.Attribution.Citations)
			}
		case *rttypes.ResponseStreamMemberTrace:
			turn.traces = append(turn.traces, e.Value)
		case *rttypes.ResponseStreamMemberReturnControl:
			rc := e.Value
			turn.returnControl = &rc
		}
	}

	require.NoError(t, stream.Err())

	return turn, nil
}

func TestSDKInvokeAgentResolvesState(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agentID := s.newAgent(t, "support", nil)
	otherID := s.newAgent(t, "other", nil)

	alias, err := s.agent.CreateAgentAlias(ctx, &awsba.CreateAgentAliasInput{AgentId: aws.String(agentID), AgentAliasName: aws.String("live")})
	require.NoError(t, err)

	aliasID := aws.ToString(alias.AgentAlias.AgentAliasId)
	in := func(agent, alias string) *agentruntime.InvokeAgentInput {
		return &agentruntime.InvokeAgentInput{
			AgentId: aws.String(agent), AgentAliasId: aws.String(alias), SessionId: aws.String("s-1"),
			InputText: aws.String("hello"),
		}
	}

	turn, err := invoke(t, s, in(agentID, aliasID))
	require.NoError(t, err)
	assert.Contains(t, turn.text, "hello")
	assert.Equal(t, "s-1", turn.sessionID)

	_, err = invoke(t, s, in(agentID, "TSTALIASID"))
	require.NoError(t, err, "TSTALIASID routes to DRAFT")

	_, err = s.runtime.InvokeAgent(ctx, in("MISSING000", "TSTALIASID"))
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)

	_, err = s.runtime.InvokeAgent(ctx, in(agentID, "MISSING000"))
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)

	_, err = s.runtime.InvokeAgent(ctx, in(otherID, aliasID))
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)

	_, err = s.runtime.InvokeAgent(ctx, in("bad id!", "TSTALIASID"))
	requireAPIError(t, err, "ValidationException", http.StatusBadRequest)

	_, err = s.agent.UpdateAgentAlias(ctx, &awsba.UpdateAgentAliasInput{
		AgentId: aws.String(agentID), AgentAliasId: aws.String(aliasID), AgentAliasName: aws.String("live"),
		AliasInvocationState: batypes.AliasInvocationStateRejectInvocations,
		RoutingConfiguration: []batypes.AgentAliasRoutingConfigurationListItem{{AgentVersion: aws.String("1")}},
	})
	require.NoError(t, err)

	_, err = s.runtime.InvokeAgent(ctx, in(agentID, aliasID))
	requireAPIError(t, err, "ValidationException", http.StatusBadRequest)
}

func TestSDKInvokeAgentTraceAndKnowledgeBase(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agentID := s.newAgent(t, "support", nil)
	kbID, _ := s.newKnowledgeBase(t, "faq", map[string]string{
		"refunds":  "Refunds are processed within 5 business days.",
		"shipping": "Shipping takes 2 days.",
	})

	_, err := s.agent.AssociateAgentKnowledgeBase(ctx, &awsba.AssociateAgentKnowledgeBaseInput{
		AgentId: aws.String(agentID), AgentVersion: aws.String("DRAFT"), KnowledgeBaseId: aws.String(kbID),
		Description: aws.String("FAQ"),
	})
	require.NoError(t, err)
	s.prepare(t, agentID)

	quiet, err := invoke(t, s, &agentruntime.InvokeAgentInput{
		AgentId: aws.String(agentID), AgentAliasId: aws.String("TSTALIASID"), SessionId: aws.String("s-1"),
		InputText: aws.String("how long do refunds take"),
	})
	require.NoError(t, err)
	assert.Empty(t, quiet.traces, "no traces without enableTrace")
	assert.Equal(t, 1, quiet.citations, "the answer cites the knowledge base")
	assert.Contains(t, quiet.text, "Refunds are processed within 5 business days.")

	traced, err := invoke(t, s, &agentruntime.InvokeAgentInput{
		AgentId: aws.String(agentID), AgentAliasId: aws.String("TSTALIASID"), SessionId: aws.String("s-2"),
		InputText: aws.String("how long do refunds take"), EnableTrace: aws.Bool(true),
	})
	require.NoError(t, err)
	require.NotEmpty(t, traced.traces)

	var sawFinal, sawModelInput, sawKBReference bool

	for _, tp := range traced.traces {
		assert.Equal(t, agentID, aws.ToString(tp.AgentId))
		assert.Equal(t, "DRAFT", aws.ToString(tp.AgentVersion))

		orch, ok := tp.Trace.(*rttypes.TraceMemberOrchestrationTrace)
		require.True(t, ok, "orchestration traces")

		switch o := orch.Value.(type) {
		case *rttypes.OrchestrationTraceMemberModelInvocationInput:
			sawModelInput = aws.ToString(o.Value.FoundationModel) == claudeModel
		case *rttypes.OrchestrationTraceMemberObservation:
			switch o.Value.Type {
			case rttypes.TypeFinish:
				sawFinal = aws.ToString(o.Value.FinalResponse.Text) == traced.text
			case rttypes.TypeKnowledgeBase:
				refs := o.Value.KnowledgeBaseLookupOutput.RetrievedReferences
				sawKBReference = len(refs) > 0 && aws.ToString(refs[0].Content.Text) == "Refunds are processed within 5 business days."
			default:
			}
		}
	}

	assert.True(t, sawModelInput, "a modelInvocationInput trace names the agent's model")
	assert.True(t, sawFinal, "the FINISH observation carries the answer")
	assert.True(t, sawKBReference, "the knowledge-base observation lists the retrieved chunks")
}

func TestSDKInvokeAgentPreAndPostProcessingTraces(t *testing.T) {
	s := newStack(t)
	agentID := s.newAgent(t, "processing", func(in *awsba.CreateAgentInput) {
		in.PromptOverrideConfiguration = &batypes.PromptOverrideConfiguration{PromptConfigurations: []batypes.PromptConfiguration{
			{PromptType: batypes.PromptTypePreProcessing, PromptState: batypes.PromptStateEnabled, PromptCreationMode: batypes.CreationModeDefault},
			{PromptType: batypes.PromptTypePostProcessing, PromptState: batypes.PromptStateEnabled, PromptCreationMode: batypes.CreationModeDefault},
		}}
	})

	turn, err := invoke(t, s, &agentruntime.InvokeAgentInput{
		AgentId: aws.String(agentID), AgentAliasId: aws.String("TSTALIASID"), SessionId: aws.String("s-1"),
		InputText: aws.String("hello"), EnableTrace: aws.Bool(true),
	})
	require.NoError(t, err)
	require.NotEmpty(t, turn.traces)

	_, first := turn.traces[0].Trace.(*rttypes.TraceMemberPreProcessingTrace)
	assert.True(t, first, "pre-processing runs before orchestration")

	last, ok := turn.traces[len(turn.traces)-1].Trace.(*rttypes.TraceMemberPostProcessingTrace)
	require.True(t, ok, "post-processing runs last")

	out, ok := last.Value.(*rttypes.PostProcessingTraceMemberModelInvocationOutput)
	require.True(t, ok)
	assert.Equal(t, turn.text, aws.ToString(out.Value.ParsedResponse.Text))

	plain, err := invoke(t, s, &agentruntime.InvokeAgentInput{
		AgentId: aws.String(s.newAgent(t, "default", nil)), AgentAliasId: aws.String("TSTALIASID"),
		SessionId: aws.String("s-1"), InputText: aws.String("hello"), EnableTrace: aws.Bool(true),
	})
	require.NoError(t, err)

	for _, tp := range plain.traces {
		_, orch := tp.Trace.(*rttypes.TraceMemberOrchestrationTrace)
		assert.True(t, orch, "pre- and post-processing are off unless the agent enables them")
	}
}

func TestSDKInvokeAgentReturnControl(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agentID := s.newAgent(t, "orders", nil)

	_, err := s.agent.CreateAgentActionGroup(ctx, &awsba.CreateAgentActionGroupInput{
		AgentId: aws.String(agentID), AgentVersion: aws.String("DRAFT"), ActionGroupName: aws.String("orders"),
		ActionGroupExecutor: &batypes.ActionGroupExecutorMemberCustomControl{Value: batypes.CustomControlMethodReturnControl},
		FunctionSchema: &batypes.FunctionSchemaMemberFunctions{Value: []batypes.Function{{
			Name:       aws.String("get_order"),
			Parameters: map[string]batypes.ParameterDetail{"order_id": {Type: batypes.TypeString, Required: aws.Bool(true)}},
		}}},
	})
	require.NoError(t, err)

	_, err = s.runtime.InvokeAgent(ctx, &agentruntime.InvokeAgentInput{
		AgentId: aws.String(agentID), AgentAliasId: aws.String("TSTALIASID"), SessionId: aws.String("s-1"),
		InputText: aws.String("hello"),
	})
	requireAPIError(t, err, "ValidationException", http.StatusBadRequest)

	s.prepare(t, agentID)

	first, err := invoke(t, s, &agentruntime.InvokeAgentInput{
		AgentId: aws.String(agentID), AgentAliasId: aws.String("TSTALIASID"), SessionId: aws.String("s-1"),
		InputText: aws.String("get order order_id=42 please"),
	})
	require.NoError(t, err)
	require.NotNil(t, first.returnControl, "a RETURN_CONTROL action group hands control back")
	assert.Empty(t, first.text)

	fn, ok := first.returnControl.InvocationInputs[0].(*rttypes.InvocationInputMemberMemberFunctionInvocationInput)
	require.True(t, ok)
	assert.Equal(t, "get_order", aws.ToString(fn.Value.Function))
	require.Len(t, fn.Value.Parameters, 1)
	assert.Equal(t, "42", aws.ToString(fn.Value.Parameters[0].Value))

	invocationID := first.returnControl.InvocationId

	_, err = s.runtime.InvokeAgent(ctx, &agentruntime.InvokeAgentInput{
		AgentId: aws.String(agentID), AgentAliasId: aws.String("TSTALIASID"), SessionId: aws.String("s-1"),
		SessionState: &rttypes.SessionState{
			InvocationId: aws.String("wrong"),
			ReturnControlInvocationResults: []rttypes.InvocationResultMember{&rttypes.InvocationResultMemberMemberFunctionResult{
				Value: rttypes.FunctionResult{ActionGroup: aws.String("orders"), Function: aws.String("get_order")},
			}},
		},
	})
	requireAPIError(t, err, "ValidationException", http.StatusBadRequest)

	second, err := invoke(t, s, &agentruntime.InvokeAgentInput{
		AgentId: aws.String(agentID), AgentAliasId: aws.String("TSTALIASID"), SessionId: aws.String("s-1"),
		SessionState: &rttypes.SessionState{
			InvocationId: invocationID,
			ReturnControlInvocationResults: []rttypes.InvocationResultMember{&rttypes.InvocationResultMemberMemberFunctionResult{
				Value: rttypes.FunctionResult{
					ActionGroup: aws.String("orders"), Function: aws.String("get_order"),
					ResponseBody: map[string]rttypes.ContentBody{"TEXT": {Body: aws.String("Order 42 shipped yesterday.")}},
				},
			}},
		},
	})
	require.NoError(t, err)
	assert.Nil(t, second.returnControl)
	assert.True(t, strings.Contains(second.text, "Order 42 shipped yesterday."), "the result reaches the answer: %q", second.text)
}

func TestSDKInvokeAgentLambdaActionGroup(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	_, err := s.provider.Lambda.CreateFunction(ctx, serverlessdriver.FunctionConfig{
		Name: "weather", Runtime: "python3.12", Handler: "index.handler", Role: roleARN,
	})
	require.NoError(t, err)

	var event map[string]any

	s.provider.Lambda.RegisterHandler("weather", func(_ context.Context, payload []byte) ([]byte, error) {
		require.NoError(t, json.Unmarshal(payload, &event))

		return []byte(`{"messageVersion":"1.0","response":{"actionGroup":"tools","function":"get_weather",` +
			`"functionResponse":{"responseBody":{"TEXT":{"body":"It is sunny in Paris."}}}}}`), nil
	})

	agentID := s.newAgent(t, "weather", nil)
	addLambdaGroup := func(arn string) {
		_, err := s.agent.CreateAgentActionGroup(ctx, &awsba.CreateAgentActionGroupInput{
			AgentId: aws.String(agentID), AgentVersion: aws.String("DRAFT"), ActionGroupName: aws.String("tools"),
			ActionGroupExecutor: &batypes.ActionGroupExecutorMemberLambda{Value: arn},
			FunctionSchema: &batypes.FunctionSchemaMemberFunctions{Value: []batypes.Function{{
				Name:       aws.String("get_weather"),
				Parameters: map[string]batypes.ParameterDetail{"city": {Type: batypes.TypeString}},
			}}},
		})
		require.NoError(t, err)
		s.prepare(t, agentID)
	}

	addLambdaGroup("arn:aws:lambda:us-east-1:123456789012:function:weather")

	turn, err := invoke(t, s, &agentruntime.InvokeAgentInput{
		AgentId: aws.String(agentID), AgentAliasId: aws.String("TSTALIASID"), SessionId: aws.String("s-1"),
		InputText: aws.String("get weather for city=Paris"),
	})
	require.NoError(t, err)
	assert.Contains(t, turn.text, "It is sunny in Paris.")
	assert.Equal(t, "1.0", event["messageVersion"])
	assert.Equal(t, "get_weather", event["function"])
	assert.Equal(t, "tools", event["actionGroup"])
	assert.Equal(t, []any{map[string]any{"name": "city", "type": "string", "value": "Paris"}}, event["parameters"],
		"the agent event uses the documented lowercase parameter fields")

	broken := s.newAgent(t, "broken", nil)
	agentID = broken
	addLambdaGroup("arn:aws:lambda:us-east-1:123456789012:function:missing")

	_, err = s.runtime.InvokeAgent(ctx, &agentruntime.InvokeAgentInput{
		AgentId: aws.String(broken), AgentAliasId: aws.String("TSTALIASID"), SessionId: aws.String("s-1"),
		InputText: aws.String("get weather for city=Paris"),
	})
	requireAPIError(t, err, "DependencyFailedException", http.StatusFailedDependency)
}

func TestSDKInvokeAgentStreamFinalResponse(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agentID := s.newAgent(t, "streamer", nil)
	in := func(session string, stream bool) *agentruntime.InvokeAgentInput {
		return &agentruntime.InvokeAgentInput{
			AgentId: aws.String(agentID), AgentAliasId: aws.String("TSTALIASID"), SessionId: aws.String(session),
			InputText:               aws.String("please summarize the refund policy for orders placed last month"),
			StreamingConfigurations: &rttypes.StreamingConfigurations{StreamFinalResponse: stream},
		}
	}

	chunks := func(session string, stream bool) []string {
		out, err := s.runtime.InvokeAgent(ctx, in(session, stream))
		require.NoError(t, err)

		events := out.GetStream()
		defer events.Close()

		var got []string

		for ev := range events.Events() {
			if c, ok := ev.(*rttypes.ResponseStreamMemberChunk); ok {
				got = append(got, string(c.Value.Bytes))
			}
		}

		require.NoError(t, events.Err())

		return got
	}

	whole := chunks("s-1", false)
	require.Len(t, whole, 1)

	pieces := chunks("s-2", true)
	assert.Greater(t, len(pieces), 1, "streamFinalResponse streams the answer in pieces")
	assert.Equal(t, whole[0], strings.Join(pieces, ""), "the pieces add up to the same answer")
}
