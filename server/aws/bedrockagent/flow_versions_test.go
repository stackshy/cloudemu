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

func createFlow(t *testing.T, client *awsba.Client, name string) (id, arn string) {
	t.Helper()

	out, err := client.CreateFlow(context.Background(), &awsba.CreateFlowInput{
		Name: aws.String(name), ExecutionRoleArn: aws.String(roleArn),
	})
	require.NoError(t, err)
	requireStatus(t, out.ResultMetadata, http.StatusCreated)

	return aws.ToString(out.Id), aws.ToString(out.Arn)
}

func TestSDKFlowVersionsAndAliases(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()
	flowID, flowArn := createFlow(t, client, "pipeline")

	v1, err := client.CreateFlowVersion(ctx, &awsba.CreateFlowVersionInput{FlowIdentifier: aws.String(flowID), Description: aws.String("v1")})
	require.NoError(t, err)
	requireStatus(t, v1.ResultMetadata, http.StatusCreated)
	assert.Equal(t, "1", aws.ToString(v1.Version))
	assert.Equal(t, flowArn, aws.ToString(v1.Arn), "a flow version reports the flow ARN")

	// The flow identifier may be the flow ARN.
	got, err := client.GetFlowVersion(ctx, &awsba.GetFlowVersionInput{FlowIdentifier: aws.String(flowArn), FlowVersion: aws.String("1")})
	require.NoError(t, err)
	assert.Equal(t, "v1", aws.ToString(got.Description))

	versions, err := client.ListFlowVersions(ctx, &awsba.ListFlowVersionsInput{FlowIdentifier: aws.String(flowID)})
	require.NoError(t, err)
	require.Len(t, versions.FlowVersionSummaries, 2)
	assert.Equal(t, "DRAFT", aws.ToString(versions.FlowVersionSummaries[0].Version))

	alias, err := client.CreateFlowAlias(ctx, &awsba.CreateFlowAliasInput{
		FlowIdentifier:       aws.String(flowID),
		Name:                 aws.String("live"),
		RoutingConfiguration: []batypes.FlowAliasRoutingConfigurationListItem{{FlowVersion: aws.String("1")}},
		ConcurrencyConfiguration: &batypes.FlowAliasConcurrencyConfiguration{
			Type: batypes.ConcurrencyTypeManual, MaxConcurrency: aws.Int32(5),
		},
		Tags: map[string]string{"stage": "prod"},
	})
	require.NoError(t, err)
	requireStatus(t, alias.ResultMetadata, http.StatusCreated)

	aliasID, aliasArn := aws.ToString(alias.Id), aws.ToString(alias.Arn)
	assert.Equal(t, flowArn+"/alias/"+aliasID, aliasArn)

	tags, err := client.ListTagsForResource(ctx, &awsba.ListTagsForResourceInput{ResourceArn: aws.String(aliasArn)})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"stage": "prod"}, tags.Tags)

	_, err = client.TagResource(ctx, &awsba.TagResourceInput{ResourceArn: aws.String(aliasArn), Tags: map[string]string{"team": "ml"}})
	require.NoError(t, err)

	byArn, err := client.GetFlowAlias(ctx, &awsba.GetFlowAliasInput{FlowIdentifier: aws.String(flowID), AliasIdentifier: aws.String(aliasArn)})
	require.NoError(t, err)
	assert.Equal(t, int32(5), aws.ToInt32(byArn.ConcurrencyConfiguration.MaxConcurrency))

	_, err = client.DeleteFlowVersion(ctx, &awsba.DeleteFlowVersionInput{FlowIdentifier: aws.String(flowID), FlowVersion: aws.String("1")})
	requireAPIError(t, err, "ConflictException", http.StatusConflict)

	_, err = client.CreateFlowAlias(ctx, &awsba.CreateFlowAliasInput{
		FlowIdentifier: aws.String(flowID), Name: aws.String("ghost"),
		RoutingConfiguration: []batypes.FlowAliasRoutingConfigurationListItem{{FlowVersion: aws.String("9")}},
	})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)

	_, err = client.CreateFlowAlias(ctx, &awsba.CreateFlowAliasInput{
		FlowIdentifier: aws.String(flowID), Name: aws.String("live"),
		RoutingConfiguration: []batypes.FlowAliasRoutingConfigurationListItem{{FlowVersion: aws.String("DRAFT")}},
	})
	requireAPIError(t, err, "ConflictException", http.StatusConflict)

	updated, err := client.UpdateFlowAlias(ctx, &awsba.UpdateFlowAliasInput{
		FlowIdentifier: aws.String(flowID), AliasIdentifier: aws.String(aliasID), Name: aws.String("live"),
		RoutingConfiguration: []batypes.FlowAliasRoutingConfigurationListItem{{FlowVersion: aws.String("DRAFT")}},
	})
	require.NoError(t, err)
	assert.Equal(t, "DRAFT", aws.ToString(updated.RoutingConfiguration[0].FlowVersion))
	assert.Nil(t, updated.ConcurrencyConfiguration, "an update replaces the concurrency configuration")

	aliases, err := client.ListFlowAliases(ctx, &awsba.ListFlowAliasesInput{FlowIdentifier: aws.String(flowID)})
	require.NoError(t, err)
	require.Len(t, aliases.FlowAliasSummaries, 1)

	del, err := client.DeleteFlowVersion(ctx, &awsba.DeleteFlowVersionInput{FlowIdentifier: aws.String(flowID), FlowVersion: aws.String("1")})
	require.NoError(t, err)
	assert.Equal(t, "1", aws.ToString(del.Version))

	delAlias, err := client.DeleteFlowAlias(ctx, &awsba.DeleteFlowAliasInput{FlowIdentifier: aws.String(flowID), AliasIdentifier: aws.String(aliasID)})
	require.NoError(t, err)
	assert.Equal(t, flowID, aws.ToString(delAlias.FlowId))

	_, err = client.ListTagsForResource(ctx, &awsba.ListTagsForResourceInput{ResourceArn: aws.String(aliasArn)})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)
}

// TestSDKDeleteFlowCascades checks a flow's versions and aliases go with it.
func TestSDKDeleteFlowCascades(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()
	flowID, flowArn := createFlow(t, client, "doomed")

	_, err := client.CreateFlowVersion(ctx, &awsba.CreateFlowVersionInput{FlowIdentifier: aws.String(flowID)})
	require.NoError(t, err)

	alias, err := client.CreateFlowAlias(ctx, &awsba.CreateFlowAliasInput{
		FlowIdentifier: aws.String(flowID), Name: aws.String("a"),
		RoutingConfiguration: []batypes.FlowAliasRoutingConfigurationListItem{{FlowVersion: aws.String("1")}},
	})
	require.NoError(t, err)

	_, err = client.DeleteFlow(ctx, &awsba.DeleteFlowInput{FlowIdentifier: aws.String(flowArn)})
	require.NoError(t, err)

	_, err = client.ListTagsForResource(ctx, &awsba.ListTagsForResourceInput{ResourceArn: alias.Arn})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)

	// A new flow never sees the deleted flow's version numbers.
	newID, _ := createFlow(t, client, "doomed")
	v, err := client.CreateFlowVersion(ctx, &awsba.CreateFlowVersionInput{FlowIdentifier: aws.String(newID)})
	require.NoError(t, err)
	assert.Equal(t, "1", aws.ToString(v.Version))
}
