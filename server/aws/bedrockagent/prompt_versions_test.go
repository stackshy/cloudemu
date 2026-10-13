package bedrockagent_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsba "github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSDKPromptVersions(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	created, err := client.CreatePrompt(ctx, &awsba.CreatePromptInput{Name: aws.String("greeting"), Description: aws.String("draft")})
	require.NoError(t, err)
	requireStatus(t, created.ResultMetadata, http.StatusCreated)

	promptID, promptArn := aws.ToString(created.Id), aws.ToString(created.Arn)

	v1, err := client.CreatePromptVersion(ctx, &awsba.CreatePromptVersionInput{
		PromptIdentifier: aws.String(promptID), Description: aws.String("first release"),
		Tags: map[string]string{"release": "1"},
	})
	require.NoError(t, err)
	requireStatus(t, v1.ResultMetadata, http.StatusCreated)
	assert.Equal(t, "1", aws.ToString(v1.Version))
	assert.Equal(t, promptArn+":1", aws.ToString(v1.Arn))

	v2, err := client.CreatePromptVersion(ctx, &awsba.CreatePromptVersionInput{PromptIdentifier: aws.String(promptArn)})
	require.NoError(t, err)
	assert.Equal(t, "2", aws.ToString(v2.Version))
	assert.Equal(t, "draft", aws.ToString(v2.Description), "a version without a description keeps DRAFT's")

	tags, err := client.ListTagsForResource(ctx, &awsba.ListTagsForResourceInput{ResourceArn: v1.Arn})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"release": "1"}, tags.Tags)

	got, err := client.GetPrompt(ctx, &awsba.GetPromptInput{PromptIdentifier: aws.String(promptID), PromptVersion: aws.String("1")})
	require.NoError(t, err)
	assert.Equal(t, "first release", aws.ToString(got.Description))

	// A versioned ARN names the version too.
	byArn, err := client.GetPrompt(ctx, &awsba.GetPromptInput{PromptIdentifier: v2.Arn})
	require.NoError(t, err)
	assert.Equal(t, "2", aws.ToString(byArn.Version))

	versions, err := client.ListPrompts(ctx, &awsba.ListPromptsInput{PromptIdentifier: aws.String(promptID)})
	require.NoError(t, err)
	require.Len(t, versions.PromptSummaries, 3)
	assert.Equal(t, "DRAFT", aws.ToString(versions.PromptSummaries[0].Version))

	all, err := client.ListPrompts(ctx, &awsba.ListPromptsInput{})
	require.NoError(t, err)
	assert.Len(t, all.PromptSummaries, 1, "without promptIdentifier only each prompt's DRAFT is listed")

	del, err := client.DeletePrompt(ctx, &awsba.DeletePromptInput{PromptIdentifier: aws.String(promptID), PromptVersion: aws.String("1")})
	require.NoError(t, err)
	assert.Equal(t, "1", aws.ToString(del.Version))
	assert.Equal(t, promptID, aws.ToString(del.Id))

	_, err = client.GetPrompt(ctx, &awsba.GetPromptInput{PromptIdentifier: aws.String(promptID), PromptVersion: aws.String("1")})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)

	_, err = client.ListTagsForResource(ctx, &awsba.ListTagsForResourceInput{ResourceArn: v1.Arn})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)

	draft, err := client.GetPrompt(ctx, &awsba.GetPromptInput{PromptIdentifier: aws.String(promptID)})
	require.NoError(t, err)
	assert.Equal(t, "DRAFT", aws.ToString(draft.Version), "deleting a version leaves the prompt")

	_, err = client.DeletePrompt(ctx, &awsba.DeletePromptInput{PromptIdentifier: aws.String(promptID)})
	require.NoError(t, err)

	_, err = client.ListTagsForResource(ctx, &awsba.ListTagsForResourceInput{ResourceArn: v2.Arn})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)
}
