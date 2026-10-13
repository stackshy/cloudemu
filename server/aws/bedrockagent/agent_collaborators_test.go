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

func TestSDKAgentCollaborators(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()
	supervisor := createAgent(t, client, "supervisor", batypes.AgentCollaborationSupervisor)
	worker := createAgent(t, client, "worker", "")

	alias, err := client.CreateAgentAlias(ctx, &awsba.CreateAgentAliasInput{AgentId: aws.String(worker), AgentAliasName: aws.String("live")})
	require.NoError(t, err)

	aliasArn := alias.AgentAlias.AgentAliasArn

	col, err := client.AssociateAgentCollaborator(ctx, &awsba.AssociateAgentCollaboratorInput{
		AgentId: aws.String(supervisor), AgentVersion: aws.String("DRAFT"),
		AgentDescriptor:          &batypes.AgentDescriptor{AliasArn: aliasArn},
		CollaboratorName:         aws.String("worker"),
		CollaborationInstruction: aws.String("Handle the worker tasks."),
	})
	require.NoError(t, err)

	colID := aws.ToString(col.AgentCollaborator.CollaboratorId)
	assert.Equal(t, batypes.RelayConversationHistoryDisabled, col.AgentCollaborator.RelayConversationHistory)
	assert.Equal(t, aws.ToString(aliasArn), aws.ToString(col.AgentCollaborator.AgentDescriptor.AliasArn))

	// The worker is not a supervisor.
	_, err = client.AssociateAgentCollaborator(ctx, &awsba.AssociateAgentCollaboratorInput{
		AgentId: aws.String(worker), AgentVersion: aws.String("DRAFT"),
		AgentDescriptor:          &batypes.AgentDescriptor{AliasArn: aliasArn},
		CollaboratorName:         aws.String("self"),
		CollaborationInstruction: aws.String("x"),
	})
	requireAPIError(t, err, "ValidationException", http.StatusBadRequest)

	_, err = client.AssociateAgentCollaborator(ctx, &awsba.AssociateAgentCollaboratorInput{
		AgentId: aws.String(supervisor), AgentVersion: aws.String("DRAFT"),
		AgentDescriptor: &batypes.AgentDescriptor{
			AliasArn: aws.String("arn:aws:bedrock:us-east-1:123456789012:agent-alias/" + worker + "/MISSING000"),
		},
		CollaboratorName:         aws.String("ghost"),
		CollaborationInstruction: aws.String("x"),
	})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)

	updated, err := client.UpdateAgentCollaborator(ctx, &awsba.UpdateAgentCollaboratorInput{
		AgentId: aws.String(supervisor), AgentVersion: aws.String("DRAFT"), CollaboratorId: aws.String(colID),
		AgentDescriptor:          &batypes.AgentDescriptor{AliasArn: aliasArn},
		CollaboratorName:         aws.String("worker"),
		CollaborationInstruction: aws.String("Handle every worker task."),
		RelayConversationHistory: batypes.RelayConversationHistoryToCollaborator,
	})
	require.NoError(t, err)
	assert.Equal(t, batypes.RelayConversationHistoryToCollaborator, updated.AgentCollaborator.RelayConversationHistory)

	list, err := client.ListAgentCollaborators(ctx, &awsba.ListAgentCollaboratorsInput{
		AgentId: aws.String(supervisor), AgentVersion: aws.String("DRAFT"),
	})
	require.NoError(t, err)
	require.Len(t, list.AgentCollaboratorSummaries, 1)
	assert.Equal(t, "Handle every worker task.", aws.ToString(list.AgentCollaboratorSummaries[0].CollaborationInstruction))

	_, err = client.DisassociateAgentCollaborator(ctx, &awsba.DisassociateAgentCollaboratorInput{
		AgentId: aws.String(supervisor), AgentVersion: aws.String("DRAFT"), CollaboratorId: aws.String(colID),
	})
	require.NoError(t, err)

	_, err = client.GetAgentCollaborator(ctx, &awsba.GetAgentCollaboratorInput{
		AgentId: aws.String(supervisor), AgentVersion: aws.String("DRAFT"), CollaboratorId: aws.String(colID),
	})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)
}
