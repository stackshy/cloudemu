package bedrockagentruntime_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsba "github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	batypes "github.com/aws/aws-sdk-go-v2/service/bedrockagent/types"
	agentruntime "github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime/document"
	rttypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
)

// newFlow creates a prepared Input -> Prompt -> Output flow and returns its id.
func (s *stack) newFlow(t *testing.T) string {
	t.Helper()

	ctx := context.Background()
	node := func(name string, typ batypes.FlowNodeType) batypes.FlowNode {
		return batypes.FlowNode{Name: aws.String(name), Type: typ}
	}
	conn := func(name, src, dst string) batypes.FlowConnection {
		return batypes.FlowConnection{
			Name: aws.String(name), Source: aws.String(src), Target: aws.String(dst), Type: batypes.FlowConnectionTypeData,
			Configuration: &batypes.FlowConnectionConfigurationMemberData{Value: batypes.FlowDataConnectionConfiguration{
				SourceOutput: aws.String("document"), TargetInput: aws.String("document"),
			}},
		}
	}

	flow, err := s.agent.CreateFlow(ctx, &awsba.CreateFlowInput{
		Name: aws.String("pipeline"), ExecutionRoleArn: aws.String(roleARN),
		Definition: &batypes.FlowDefinition{
			Nodes: []batypes.FlowNode{
				node("in", batypes.FlowNodeTypeInput), node("summarize", batypes.FlowNodeTypePrompt), node("out", batypes.FlowNodeTypeOutput),
			},
			Connections: []batypes.FlowConnection{conn("a", "in", "summarize"), conn("b", "summarize", "out")},
		},
	})
	require.NoError(t, err)

	return aws.ToString(flow.Id)
}

func flowInput(text string) []rttypes.FlowInput {
	return []rttypes.FlowInput{{
		NodeName: aws.String("in"), NodeOutputName: aws.String("document"),
		Content: &rttypes.FlowInputContentMemberDocument{Value: document.NewLazyDocument(text)},
	}}
}

func TestSDKInvokeFlow(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	flowID := s.newFlow(t)

	_, err := s.runtime.InvokeFlow(ctx, &agentruntime.InvokeFlowInput{
		FlowIdentifier: aws.String(flowID), FlowAliasIdentifier: aws.String("TSTALIASID"), Inputs: flowInput("hi"),
	})
	requireAPIError(t, err, "ValidationException", http.StatusBadRequest) // DRAFT is not prepared yet

	_, err = s.agent.PrepareFlow(ctx, &awsba.PrepareFlowInput{FlowIdentifier: aws.String(flowID)})
	require.NoError(t, err)

	out, err := s.runtime.InvokeFlow(ctx, &agentruntime.InvokeFlowInput{
		FlowIdentifier: aws.String(flowID), FlowAliasIdentifier: aws.String("TSTALIASID"), Inputs: flowInput("hello flow"),
		EnableTrace: aws.Bool(true),
	})
	require.NoError(t, err)
	assert.NotEmpty(t, aws.ToString(out.ExecutionId))

	stream := out.GetStream()
	defer stream.Close()

	var outputs, traces int

	var completion rttypes.FlowCompletionReason

	for ev := range stream.Events() {
		switch e := ev.(type) {
		case *rttypes.FlowResponseStreamMemberFlowOutputEvent:
			outputs++
			assert.Equal(t, "out", aws.ToString(e.Value.NodeName))

			doc, ok := e.Value.Content.(*rttypes.FlowOutputContentMemberDocument)
			require.True(t, ok)

			var text string
			require.NoError(t, doc.Value.UnmarshalSmithyDocument(&text))
			assert.Equal(t, "hello flow", text)
		case *rttypes.FlowResponseStreamMemberFlowTraceEvent:
			traces++
		case *rttypes.FlowResponseStreamMemberFlowCompletionEvent:
			completion = e.Value.CompletionReason
		}
	}

	require.NoError(t, stream.Err())
	assert.Equal(t, 1, outputs)
	assert.NotZero(t, traces)
	assert.Equal(t, rttypes.FlowCompletionReasonSuccess, completion)

	_, err = s.runtime.InvokeFlow(ctx, &agentruntime.InvokeFlowInput{
		FlowIdentifier: aws.String(flowID), FlowAliasIdentifier: aws.String("MISSING000"), Inputs: flowInput("x"),
	})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)

	_, err = s.runtime.InvokeFlow(ctx, &agentruntime.InvokeFlowInput{
		FlowIdentifier: aws.String("MISSING000"), FlowAliasIdentifier: aws.String("TSTALIASID"), Inputs: flowInput("x"),
	})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)
}

func TestSDKFlowExecutions(t *testing.T) {
	s := newStack(t, config.WithAsyncSettle())
	ctx := context.Background()
	flowID := s.newFlow(t)

	_, err := s.agent.PrepareFlow(ctx, &awsba.PrepareFlowInput{FlowIdentifier: aws.String(flowID)})
	require.NoError(t, err)

	_, err = s.agent.CreateFlowVersion(ctx, &awsba.CreateFlowVersionInput{FlowIdentifier: aws.String(flowID)})
	require.NoError(t, err)

	alias, err := s.agent.CreateFlowAlias(ctx, &awsba.CreateFlowAliasInput{
		FlowIdentifier: aws.String(flowID), Name: aws.String("live"),
		RoutingConfiguration: []batypes.FlowAliasRoutingConfigurationListItem{{FlowVersion: aws.String("1")}},
	})
	require.NoError(t, err)

	aliasID := aws.ToString(alias.Id)

	started, err := s.runtime.StartFlowExecution(ctx, &agentruntime.StartFlowExecutionInput{
		FlowIdentifier: aws.String(flowID), FlowAliasIdentifier: aws.String(aliasID), Inputs: flowInput("async"),
		FlowExecutionName: aws.String("run-1"),
	})
	require.NoError(t, err)

	arn := started.ExecutionArn
	assert.Contains(t, aws.ToString(arn), "flow/"+flowID+"/alias/"+aliasID+"/execution/run-1")

	got, err := s.runtime.GetFlowExecution(ctx, &agentruntime.GetFlowExecutionInput{
		FlowIdentifier: aws.String(flowID), FlowAliasIdentifier: aws.String(aliasID), ExecutionIdentifier: aws.String("run-1"),
	})
	require.NoError(t, err)
	assert.Equal(t, rttypes.FlowExecutionStatusRunning, got.Status)
	assert.Equal(t, "1", aws.ToString(got.FlowVersion))

	s.clock.Advance(time.Minute)

	got, err = s.runtime.GetFlowExecution(ctx, &agentruntime.GetFlowExecutionInput{
		FlowIdentifier: aws.String(flowID), FlowAliasIdentifier: aws.String(aliasID), ExecutionIdentifier: arn,
	})
	require.NoError(t, err)
	assert.Equal(t, rttypes.FlowExecutionStatusSucceeded, got.Status)
	assert.NotNil(t, got.EndedAt)

	_, err = s.runtime.StopFlowExecution(ctx, &agentruntime.StopFlowExecutionInput{
		FlowIdentifier: aws.String(flowID), FlowAliasIdentifier: aws.String(aliasID), ExecutionIdentifier: aws.String("run-1"),
	})
	requireAPIError(t, err, "ConflictException", http.StatusConflict)

	second, err := s.runtime.StartFlowExecution(ctx, &agentruntime.StartFlowExecutionInput{
		FlowIdentifier: aws.String(flowID), FlowAliasIdentifier: aws.String(aliasID), Inputs: flowInput("stop me"),
	})
	require.NoError(t, err)

	stopped, err := s.runtime.StopFlowExecution(ctx, &agentruntime.StopFlowExecutionInput{
		FlowIdentifier: aws.String(flowID), FlowAliasIdentifier: aws.String(aliasID), ExecutionIdentifier: second.ExecutionArn,
	})
	require.NoError(t, err)
	assert.Equal(t, rttypes.FlowExecutionStatusAborted, stopped.Status)

	list, err := s.runtime.ListFlowExecutions(ctx, &agentruntime.ListFlowExecutionsInput{FlowIdentifier: aws.String(flowID)})
	require.NoError(t, err)
	assert.Len(t, list.FlowExecutionSummaries, 2)

	events, err := s.runtime.ListFlowExecutionEvents(ctx, &agentruntime.ListFlowExecutionEventsInput{
		FlowIdentifier: aws.String(flowID), FlowAliasIdentifier: aws.String(aliasID), ExecutionIdentifier: aws.String("run-1"),
		EventType: rttypes.FlowExecutionEventTypeFlow,
	})
	require.NoError(t, err)
	require.Len(t, events.FlowExecutionEvents, 2, "one flow input and one flow output event")

	_, ok := events.FlowExecutionEvents[0].(*rttypes.FlowExecutionEventMemberFlowInputEvent)
	assert.True(t, ok)

	nodeEvents, err := s.runtime.ListFlowExecutionEvents(ctx, &agentruntime.ListFlowExecutionEventsInput{
		FlowIdentifier: aws.String(flowID), FlowAliasIdentifier: aws.String(aliasID), ExecutionIdentifier: aws.String("run-1"),
		EventType: rttypes.FlowExecutionEventTypeNode,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, nodeEvents.FlowExecutionEvents)

	snap, err := s.runtime.GetExecutionFlowSnapshot(ctx, &agentruntime.GetExecutionFlowSnapshotInput{
		FlowIdentifier: aws.String(flowID), FlowAliasIdentifier: aws.String(aliasID), ExecutionIdentifier: aws.String("run-1"),
	})
	require.NoError(t, err)
	assert.Contains(t, aws.ToString(snap.Definition), `"summarize"`)
	assert.Equal(t, roleARN, aws.ToString(snap.ExecutionRoleArn))

	_, err = s.runtime.GetFlowExecution(ctx, &agentruntime.GetFlowExecutionInput{
		FlowIdentifier: aws.String(flowID), FlowAliasIdentifier: aws.String(aliasID), ExecutionIdentifier: aws.String("nope"),
	})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)
}
