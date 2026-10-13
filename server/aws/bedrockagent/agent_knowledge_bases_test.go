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

func TestSDKAgentKnowledgeBaseAssociation(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()
	agentID := createAgent(t, client, "researcher", "")
	kbID := createKnowledgeBase(t, client, "docs")

	assoc, err := client.AssociateAgentKnowledgeBase(ctx, &awsba.AssociateAgentKnowledgeBaseInput{
		AgentId: aws.String(agentID), AgentVersion: aws.String("DRAFT"), KnowledgeBaseId: aws.String(kbID),
		Description: aws.String("Product docs"),
	})
	require.NoError(t, err)
	assert.Equal(t, batypes.KnowledgeBaseStateEnabled, assoc.AgentKnowledgeBase.KnowledgeBaseState)

	_, err = client.AssociateAgentKnowledgeBase(ctx, &awsba.AssociateAgentKnowledgeBaseInput{
		AgentId: aws.String(agentID), AgentVersion: aws.String("DRAFT"), KnowledgeBaseId: aws.String(kbID),
		Description: aws.String("again"),
	})
	requireAPIError(t, err, "ConflictException", http.StatusConflict)

	_, err = client.AssociateAgentKnowledgeBase(ctx, &awsba.AssociateAgentKnowledgeBaseInput{
		AgentId: aws.String(agentID), AgentVersion: aws.String("DRAFT"), KnowledgeBaseId: aws.String("MISSING000"),
		Description: aws.String("x"),
	})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)

	updated, err := client.UpdateAgentKnowledgeBase(ctx, &awsba.UpdateAgentKnowledgeBaseInput{
		AgentId: aws.String(agentID), AgentVersion: aws.String("DRAFT"), KnowledgeBaseId: aws.String(kbID),
		KnowledgeBaseState: batypes.KnowledgeBaseStateDisabled,
	})
	require.NoError(t, err)
	assert.Equal(t, batypes.KnowledgeBaseStateDisabled, updated.AgentKnowledgeBase.KnowledgeBaseState)
	assert.Equal(t, "Product docs", aws.ToString(updated.AgentKnowledgeBase.Description), "omitted description is kept")

	list, err := client.ListAgentKnowledgeBases(ctx, &awsba.ListAgentKnowledgeBasesInput{
		AgentId: aws.String(agentID), AgentVersion: aws.String("DRAFT"),
	})
	require.NoError(t, err)
	require.Len(t, list.AgentKnowledgeBaseSummaries, 1)

	del, err := client.DisassociateAgentKnowledgeBase(ctx, &awsba.DisassociateAgentKnowledgeBaseInput{
		AgentId: aws.String(agentID), AgentVersion: aws.String("DRAFT"), KnowledgeBaseId: aws.String(kbID),
	})
	require.NoError(t, err)
	requireStatus(t, del.ResultMetadata, http.StatusNoContent)

	_, err = client.GetAgentKnowledgeBase(ctx, &awsba.GetAgentKnowledgeBaseInput{
		AgentId: aws.String(agentID), AgentVersion: aws.String("DRAFT"), KnowledgeBaseId: aws.String(kbID),
	})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)
}
