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

// TestSDKAgentAliasCutsVersions drives the deploy flow real Bedrock documents:
// an alias created or updated without routingConfiguration cuts the next
// numbered version of DRAFT (with its action groups) and routes to it.
func TestSDKAgentAliasCutsVersions(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()
	agentID := createAgent(t, client, "deployer", "")

	_, err := client.CreateAgentActionGroup(ctx, &awsba.CreateAgentActionGroupInput{
		AgentId: aws.String(agentID), AgentVersion: aws.String("DRAFT"), ActionGroupName: aws.String("tools"),
		ParentActionGroupSignature: batypes.ActionGroupSignatureAmazonUserinput,
	})
	require.NoError(t, err)

	created, err := client.CreateAgentAlias(ctx, &awsba.CreateAgentAliasInput{
		AgentId: aws.String(agentID), AgentAliasName: aws.String("prod"),
	})
	require.NoError(t, err)
	requireStatus(t, created.ResultMetadata, http.StatusAccepted)

	alias := created.AgentAlias
	aliasID := aws.ToString(alias.AgentAliasId)
	require.Len(t, alias.RoutingConfiguration, 1)
	assert.Equal(t, "1", aws.ToString(alias.RoutingConfiguration[0].AgentVersion))
	assert.Equal(t, batypes.AgentAliasStatusPrepared, alias.AgentAliasStatus)
	assert.Equal(t, batypes.AliasInvocationStateAcceptInvocations, alias.AliasInvocationState)
	assert.Equal(t, "arn:aws:bedrock:us-east-1:123456789012:agent-alias/"+agentID+"/"+aliasID, aws.ToString(alias.AgentAliasArn))
	require.Len(t, alias.AgentAliasHistoryEvents, 1)

	v1, err := client.GetAgentVersion(ctx, &awsba.GetAgentVersionInput{AgentId: aws.String(agentID), AgentVersion: aws.String("1")})
	require.NoError(t, err)
	assert.Equal(t, "1", aws.ToString(v1.AgentVersion.Version))
	assert.Equal(t, batypes.AgentStatusPrepared, v1.AgentVersion.AgentStatus)
	assert.Equal(t, "deployer", aws.ToString(v1.AgentVersion.AgentName))

	groups, err := client.ListAgentActionGroups(ctx, &awsba.ListAgentActionGroupsInput{
		AgentId: aws.String(agentID), AgentVersion: aws.String("1"),
	})
	require.NoError(t, err)
	require.Len(t, groups.ActionGroupSummaries, 1, "version 1 snapshots DRAFT's action groups")

	versions, err := client.ListAgentVersions(ctx, &awsba.ListAgentVersionsInput{AgentId: aws.String(agentID)})
	require.NoError(t, err)
	require.Len(t, versions.AgentVersionSummaries, 2)
	assert.Equal(t, "DRAFT", aws.ToString(versions.AgentVersionSummaries[0].AgentVersion))
	assert.Equal(t, "1", aws.ToString(versions.AgentVersionSummaries[1].AgentVersion))

	// Updating without routingConfiguration cuts version 2 and closes the
	// previous history event.
	updated, err := client.UpdateAgentAlias(ctx, &awsba.UpdateAgentAliasInput{
		AgentId: aws.String(agentID), AgentAliasId: aws.String(aliasID), AgentAliasName: aws.String("prod"),
		AliasInvocationState: batypes.AliasInvocationStateRejectInvocations,
	})
	require.NoError(t, err)
	requireStatus(t, updated.ResultMetadata, http.StatusAccepted)
	assert.Equal(t, "2", aws.ToString(updated.AgentAlias.RoutingConfiguration[0].AgentVersion))
	assert.Equal(t, batypes.AliasInvocationStateRejectInvocations, updated.AgentAlias.AliasInvocationState)
	require.Len(t, updated.AgentAlias.AgentAliasHistoryEvents, 2)
	assert.Nil(t, updated.AgentAlias.AgentAliasHistoryEvents[0].EndDate, "the current routing is open")
	assert.NotNil(t, updated.AgentAlias.AgentAliasHistoryEvents[1].EndDate, "the previous routing is closed")

	// Route back to version 1 explicitly; no version is cut.
	_, err = client.UpdateAgentAlias(ctx, &awsba.UpdateAgentAliasInput{
		AgentId: aws.String(agentID), AgentAliasId: aws.String(aliasID), AgentAliasName: aws.String("prod"),
		RoutingConfiguration: []batypes.AgentAliasRoutingConfigurationListItem{{AgentVersion: aws.String("1")}},
	})
	require.NoError(t, err)

	versions, err = client.ListAgentVersions(ctx, &awsba.ListAgentVersionsInput{AgentId: aws.String(agentID)})
	require.NoError(t, err)
	assert.Len(t, versions.AgentVersionSummaries, 3)

	// Version 1 is in use by the alias.
	_, err = client.DeleteAgentVersion(ctx, &awsba.DeleteAgentVersionInput{AgentId: aws.String(agentID), AgentVersion: aws.String("1")})
	requireAPIError(t, err, "ConflictException", http.StatusConflict)

	// Version 2 is not.
	del, err := client.DeleteAgentVersion(ctx, &awsba.DeleteAgentVersionInput{AgentId: aws.String(agentID), AgentVersion: aws.String("2")})
	require.NoError(t, err)
	requireStatus(t, del.ResultMetadata, http.StatusAccepted)
	assert.Equal(t, batypes.AgentStatusDeleting, del.AgentStatus)

	_, err = client.DeleteAgentVersion(ctx, &awsba.DeleteAgentVersionInput{
		AgentId: aws.String(agentID), AgentVersion: aws.String("1"), SkipResourceInUseCheck: true,
	})
	require.NoError(t, err)

	_, err = client.GetAgentVersion(ctx, &awsba.GetAgentVersionInput{AgentId: aws.String(agentID), AgentVersion: aws.String("1")})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)

	delAlias, err := client.DeleteAgentAlias(ctx, &awsba.DeleteAgentAliasInput{AgentId: aws.String(agentID), AgentAliasId: aws.String(aliasID)})
	require.NoError(t, err)
	assert.Equal(t, batypes.AgentAliasStatusDeleting, delAlias.AgentAliasStatus)

	_, err = client.GetAgentAlias(ctx, &awsba.GetAgentAliasInput{AgentId: aws.String(agentID), AgentAliasId: aws.String(aliasID)})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)
}

// TestSDKAgentTestAlias covers TSTALIASID, the alias every agent has.
func TestSDKAgentTestAlias(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()
	agentID := createAgent(t, client, "tester", "")

	got, err := client.GetAgentAlias(ctx, &awsba.GetAgentAliasInput{AgentId: aws.String(agentID), AgentAliasId: aws.String("TSTALIASID")})
	require.NoError(t, err)
	assert.Equal(t, "AgentTestAlias", aws.ToString(got.AgentAlias.AgentAliasName))
	assert.Equal(t, "DRAFT", aws.ToString(got.AgentAlias.RoutingConfiguration[0].AgentVersion))

	list, err := client.ListAgentAliases(ctx, &awsba.ListAgentAliasesInput{AgentId: aws.String(agentID)})
	require.NoError(t, err)
	require.Len(t, list.AgentAliasSummaries, 1)
	assert.Equal(t, "TSTALIASID", aws.ToString(list.AgentAliasSummaries[0].AgentAliasId))

	_, err = client.DeleteAgentAlias(ctx, &awsba.DeleteAgentAliasInput{AgentId: aws.String(agentID), AgentAliasId: aws.String("TSTALIASID")})
	requireAPIError(t, err, "ValidationException", http.StatusBadRequest)

	_, err = client.UpdateAgentAlias(ctx, &awsba.UpdateAgentAliasInput{
		AgentId: aws.String(agentID), AgentAliasId: aws.String("TSTALIASID"), AgentAliasName: aws.String("x"),
	})
	requireAPIError(t, err, "ValidationException", http.StatusBadRequest)

	_, err = client.CreateAgentAlias(ctx, &awsba.CreateAgentAliasInput{
		AgentId: aws.String(agentID), AgentAliasName: aws.String("AgentTestAlias"),
	})
	requireAPIError(t, err, "ConflictException", http.StatusConflict)
}

func TestSDKAgentAliasErrors(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()
	agentID := createAgent(t, client, "errs", "")

	_, err := client.CreateAgentAlias(ctx, &awsba.CreateAgentAliasInput{
		AgentId: aws.String(agentID), AgentAliasName: aws.String("live"),
		RoutingConfiguration: []batypes.AgentAliasRoutingConfigurationListItem{{AgentVersion: aws.String("7")}},
	})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)

	_, err = client.CreateAgentAlias(ctx, &awsba.CreateAgentAliasInput{AgentId: aws.String(agentID), AgentAliasName: aws.String("bad name")})
	requireAPIError(t, err, "ValidationException", http.StatusBadRequest)

	_, err = client.CreateAgentAlias(ctx, &awsba.CreateAgentAliasInput{AgentId: aws.String(agentID), AgentAliasName: aws.String("live")})
	require.NoError(t, err)

	_, err = client.CreateAgentAlias(ctx, &awsba.CreateAgentAliasInput{AgentId: aws.String(agentID), AgentAliasName: aws.String("live")})
	requireAPIError(t, err, "ConflictException", http.StatusConflict)

	_, err = client.GetAgentVersion(ctx, &awsba.GetAgentVersionInput{AgentId: aws.String(agentID), AgentVersion: aws.String("DRAFT")})
	requireAPIError(t, err, "ValidationException", http.StatusBadRequest)

	_, err = client.ListAgentAliases(ctx, &awsba.ListAgentAliasesInput{AgentId: aws.String("MISSING000")})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)

	// Paging: TSTALIASID plus "live", one per page.
	page, err := client.ListAgentAliases(ctx, &awsba.ListAgentAliasesInput{AgentId: aws.String(agentID), MaxResults: aws.Int32(1)})
	require.NoError(t, err)
	require.Len(t, page.AgentAliasSummaries, 1)
	require.NotNil(t, page.NextToken)

	page, err = client.ListAgentAliases(ctx, &awsba.ListAgentAliasesInput{
		AgentId: aws.String(agentID), MaxResults: aws.Int32(1), NextToken: page.NextToken,
	})
	require.NoError(t, err)
	require.Len(t, page.AgentAliasSummaries, 1)
	assert.Equal(t, "live", aws.ToString(page.AgentAliasSummaries[0].AgentAliasName))
	assert.Nil(t, page.NextToken)
}
