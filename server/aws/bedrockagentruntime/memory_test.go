package bedrockagentruntime_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsba "github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	batypes "github.com/aws/aws-sdk-go-v2/service/bedrockagent/types"
	agentruntime "github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
	rttypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSDKAgentMemory(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agentID := s.newAgent(t, "memo", func(in *awsba.CreateAgentInput) {
		in.MemoryConfiguration = &batypes.MemoryConfiguration{
			EnabledMemoryTypes: []batypes.MemoryType{batypes.MemoryTypeSessionSummary}, StorageDays: aws.Int32(10),
		}
	})

	for _, text := range []string{"I like window seats", "book a flight to Rome"} {
		_, err := invoke(t, s, &agentruntime.InvokeAgentInput{
			AgentId: aws.String(agentID), AgentAliasId: aws.String("TSTALIASID"), SessionId: aws.String("s-1"),
			MemoryId: aws.String("user-1"), InputText: aws.String(text),
		})
		require.NoError(t, err)
	}

	mem, err := s.runtime.GetAgentMemory(ctx, &agentruntime.GetAgentMemoryInput{
		AgentId: aws.String(agentID), AgentAliasId: aws.String("TSTALIASID"), MemoryId: aws.String("user-1"),
		MemoryType: rttypes.MemoryTypeSessionSummary,
	})
	require.NoError(t, err)
	assert.Empty(t, mem.MemoryContents, "a summary is written when the session ends")

	_, err = invoke(t, s, &agentruntime.InvokeAgentInput{
		AgentId: aws.String(agentID), AgentAliasId: aws.String("TSTALIASID"), SessionId: aws.String("s-1"),
		MemoryId: aws.String("user-1"), InputText: aws.String("thanks"), EndSession: aws.Bool(true),
	})
	require.NoError(t, err)

	mem, err = s.runtime.GetAgentMemory(ctx, &agentruntime.GetAgentMemoryInput{
		AgentId: aws.String(agentID), AgentAliasId: aws.String("TSTALIASID"), MemoryId: aws.String("user-1"),
		MemoryType: rttypes.MemoryTypeSessionSummary,
	})
	require.NoError(t, err)
	require.Len(t, mem.MemoryContents, 1)

	summary := mem.MemoryContents[0].(*rttypes.MemoryMemberSessionSummary).Value
	assert.Equal(t, "s-1", aws.ToString(summary.SessionId))
	assert.Contains(t, aws.ToString(summary.SummaryText), "window seats")
	assert.Equal(t, 10*24.0, summary.SessionExpiryTime.Sub(*summary.SessionStartTime).Hours())

	_, err = s.runtime.DeleteAgentMemory(ctx, &agentruntime.DeleteAgentMemoryInput{
		AgentId: aws.String(agentID), AgentAliasId: aws.String("TSTALIASID"), MemoryId: aws.String("user-1"),
	})
	require.NoError(t, err)

	mem, err = s.runtime.GetAgentMemory(ctx, &agentruntime.GetAgentMemoryInput{
		AgentId: aws.String(agentID), AgentAliasId: aws.String("TSTALIASID"), MemoryId: aws.String("user-1"),
		MemoryType: rttypes.MemoryTypeSessionSummary,
	})
	require.NoError(t, err)
	assert.Empty(t, mem.MemoryContents)

	_, err = s.runtime.GetAgentMemory(ctx, &agentruntime.GetAgentMemoryInput{
		AgentId: aws.String("MISSING000"), AgentAliasId: aws.String("TSTALIASID"), MemoryId: aws.String("user-1"),
		MemoryType: rttypes.MemoryTypeSessionSummary,
	})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)
}
