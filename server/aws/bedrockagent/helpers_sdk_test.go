package bedrockagent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awsba "github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	batypes "github.com/aws/aws-sdk-go-v2/service/bedrockagent/types"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/stretchr/testify/require"
)

// requireAPIError asserts err is the named service exception with the given
// HTTP status, as the SDK deserialized it.
func requireAPIError(t *testing.T, err error, code string, status int) {
	t.Helper()

	require.Error(t, err)

	var apiErr smithy.APIError
	require.Truef(t, errors.As(err, &apiErr), "not an API error: %v", err)
	require.Equal(t, code, apiErr.ErrorCode(), apiErr.ErrorMessage())

	var respErr *awshttp.ResponseError
	require.True(t, errors.As(err, &respErr))
	require.Equal(t, status, respErr.HTTPStatusCode())
}

// requireStatus asserts the HTTP status of a successful SDK call.
func requireStatus(t *testing.T, md middleware.Metadata, status int) {
	t.Helper()

	raw, ok := awsmiddleware.GetRawResponse(md).(*smithyhttp.Response)
	require.True(t, ok)
	require.Equal(t, status, raw.StatusCode)
}

// createAgent creates an agent with the given agentCollaboration ("" for the
// default) and returns its id.
func createAgent(t *testing.T, client *awsba.Client, name string, collaboration batypes.AgentCollaboration) string {
	t.Helper()

	out, err := client.CreateAgent(context.Background(), &awsba.CreateAgentInput{
		AgentName:            aws.String(name),
		AgentResourceRoleArn: aws.String(roleArn),
		FoundationModel:      aws.String(claudeModel),
		Instruction:          aws.String("You are a helpful assistant that answers questions."),
		AgentCollaboration:   collaboration,
	})
	require.NoError(t, err)

	return aws.ToString(out.Agent.AgentId)
}

// createKnowledgeBase creates a VECTOR knowledge base and returns its id.
func createKnowledgeBase(t *testing.T, client *awsba.Client, name string) string {
	t.Helper()

	out, err := client.CreateKnowledgeBase(context.Background(), &awsba.CreateKnowledgeBaseInput{
		Name:    aws.String(name),
		RoleArn: aws.String(roleArn),
		KnowledgeBaseConfiguration: &batypes.KnowledgeBaseConfiguration{
			Type: batypes.KnowledgeBaseTypeVector,
			VectorKnowledgeBaseConfiguration: &batypes.VectorKnowledgeBaseConfiguration{
				EmbeddingModelArn: aws.String(embedArn),
			},
		},
		StorageConfiguration: &batypes.StorageConfiguration{
			Type: batypes.KnowledgeBaseStorageTypeOpensearchServerless,
			OpensearchServerlessConfiguration: &batypes.OpenSearchServerlessConfiguration{
				CollectionArn:   aws.String("arn:aws:aoss:us-east-1:123456789012:collection/abc"),
				VectorIndexName: aws.String("idx"),
				FieldMapping: &batypes.OpenSearchServerlessFieldMapping{
					MetadataField: aws.String("m"), TextField: aws.String("t"), VectorField: aws.String("v"),
				},
			},
		},
	})
	require.NoError(t, err)

	return aws.ToString(out.KnowledgeBase.KnowledgeBaseId)
}
