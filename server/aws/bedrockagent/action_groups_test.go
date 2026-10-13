package bedrockagent_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsba "github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	batypes "github.com/aws/aws-sdk-go-v2/service/bedrockagent/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSDKActionGroupLifecycle(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()
	agentID := createAgent(t, client, "tooling", "")

	_, err := client.PrepareAgent(ctx, &awsba.PrepareAgentInput{AgentId: aws.String(agentID)})
	require.NoError(t, err)

	created, err := client.CreateAgentActionGroup(ctx, &awsba.CreateAgentActionGroupInput{
		AgentId: aws.String(agentID), AgentVersion: aws.String("DRAFT"), ActionGroupName: aws.String("weather"),
		Description: aws.String("Looks up the weather"),
		ActionGroupExecutor: &batypes.ActionGroupExecutorMemberLambda{
			Value: "arn:aws:lambda:us-east-1:123456789012:function:weather",
		},
		FunctionSchema: &batypes.FunctionSchemaMemberFunctions{Value: []batypes.Function{{
			Name: aws.String("get_weather"),
			Parameters: map[string]batypes.ParameterDetail{
				"city": {Type: batypes.TypeString, Required: aws.Bool(true)},
			},
		}}},
	})
	require.NoError(t, err)

	group := created.AgentActionGroup
	groupID := aws.ToString(group.ActionGroupId)
	assert.Len(t, groupID, 10)
	assert.Equal(t, batypes.ActionGroupStateEnabled, group.ActionGroupState)
	assert.Equal(t, "DRAFT", aws.ToString(group.AgentVersion))

	executor, ok := group.ActionGroupExecutor.(*batypes.ActionGroupExecutorMemberLambda)
	require.True(t, ok, "executor union round-trips")
	assert.Equal(t, "arn:aws:lambda:us-east-1:123456789012:function:weather", executor.Value)

	fns, ok := group.FunctionSchema.(*batypes.FunctionSchemaMemberFunctions)
	require.True(t, ok, "function schema union round-trips")
	assert.Equal(t, "get_weather", aws.ToString(fns.Value[0].Name))

	agent, err := client.GetAgent(ctx, &awsba.GetAgentInput{AgentId: aws.String(agentID)})
	require.NoError(t, err)
	assert.Equal(t, batypes.AgentStatusNotPrepared, agent.Agent.AgentStatus, "a DRAFT change needs a new PrepareAgent")

	_, err = client.CreateAgentActionGroup(ctx, &awsba.CreateAgentActionGroupInput{
		AgentId: aws.String(agentID), AgentVersion: aws.String("DRAFT"), ActionGroupName: aws.String("weather"),
	})
	requireAPIError(t, err, "ConflictException", http.StatusConflict)

	_, err = client.CreateAgentActionGroup(ctx, &awsba.CreateAgentActionGroupInput{
		AgentId: aws.String(agentID), AgentVersion: aws.String("1"), ActionGroupName: aws.String("other"),
	})
	requireAPIError(t, err, "ValidationException", http.StatusBadRequest)

	_, err = client.CreateAgentActionGroup(ctx, &awsba.CreateAgentActionGroupInput{
		AgentId: aws.String("MISSING000"), AgentVersion: aws.String("DRAFT"), ActionGroupName: aws.String("x"),
	})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)

	updated, err := client.UpdateAgentActionGroup(ctx, &awsba.UpdateAgentActionGroupInput{
		AgentId: aws.String(agentID), AgentVersion: aws.String("DRAFT"), ActionGroupId: aws.String(groupID),
		ActionGroupName: aws.String("weather"), ActionGroupState: batypes.ActionGroupStateDisabled,
	})
	require.NoError(t, err)
	assert.Equal(t, batypes.ActionGroupStateDisabled, updated.AgentActionGroup.ActionGroupState)

	got, err := client.GetAgentActionGroup(ctx, &awsba.GetAgentActionGroupInput{
		AgentId: aws.String(agentID), AgentVersion: aws.String("DRAFT"), ActionGroupId: aws.String(groupID),
	})
	require.NoError(t, err)
	assert.Equal(t, batypes.ActionGroupStateDisabled, got.AgentActionGroup.ActionGroupState)

	list, err := client.ListAgentActionGroups(ctx, &awsba.ListAgentActionGroupsInput{
		AgentId: aws.String(agentID), AgentVersion: aws.String("DRAFT"),
	})
	require.NoError(t, err)
	require.Len(t, list.ActionGroupSummaries, 1)
	assert.Equal(t, "weather", aws.ToString(list.ActionGroupSummaries[0].ActionGroupName))

	del, err := client.DeleteAgentActionGroup(ctx, &awsba.DeleteAgentActionGroupInput{
		AgentId: aws.String(agentID), AgentVersion: aws.String("DRAFT"), ActionGroupId: aws.String(groupID),
	})
	require.NoError(t, err)
	requireStatus(t, del.ResultMetadata, http.StatusNoContent)

	_, err = client.GetAgentActionGroup(ctx, &awsba.GetAgentActionGroupInput{
		AgentId: aws.String(agentID), AgentVersion: aws.String("DRAFT"), ActionGroupId: aws.String(groupID),
	})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)
}
