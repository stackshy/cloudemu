package bedrockagent_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsba "github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	providerba "github.com/stackshy/cloudemu/v2/providers/aws/bedrockagent"
	serverba "github.com/stackshy/cloudemu/v2/server/aws/bedrockagent"
)

const kbPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"111122223333"},` +
	`"Action":"bedrock:Retrieve","Resource":"*"}]}`

func TestSDKResourcePolicy(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()
	kbID := createKnowledgeBase(t, client, "shared")

	kb, err := client.GetKnowledgeBase(ctx, &awsba.GetKnowledgeBaseInput{KnowledgeBaseId: aws.String(kbID)})
	require.NoError(t, err)

	arn := kb.KnowledgeBase.KnowledgeBaseArn

	_, err = client.GetResourcePolicy(ctx, &awsba.GetResourcePolicyInput{ResourceArn: arn})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)

	put, err := client.PutResourcePolicy(ctx, &awsba.PutResourcePolicyInput{ResourceArn: arn, Policy: aws.String(kbPolicy)})
	require.NoError(t, err)
	require.NotEmpty(t, aws.ToString(put.RevisionId))

	got, err := client.GetResourcePolicy(ctx, &awsba.GetResourcePolicyInput{ResourceArn: arn})
	require.NoError(t, err)
	assert.JSONEq(t, kbPolicy, aws.ToString(got.Policy))
	assert.Equal(t, aws.ToString(put.RevisionId), aws.ToString(got.RevisionId))

	_, err = client.PutResourcePolicy(ctx, &awsba.PutResourcePolicyInput{
		ResourceArn: arn, Policy: aws.String(kbPolicy), ExpectedRevisionId: aws.String("STALE00000"),
	})
	requireAPIError(t, err, "ConflictException", http.StatusConflict)

	_, err = client.PutResourcePolicy(ctx, &awsba.PutResourcePolicyInput{ResourceArn: arn, Policy: aws.String("not json")})
	requireAPIError(t, err, "ValidationException", http.StatusBadRequest)

	del, err := client.DeleteResourcePolicy(ctx, &awsba.DeleteResourcePolicyInput{ResourceArn: arn, ExpectedRevisionId: put.RevisionId})
	require.NoError(t, err)
	assert.Equal(t, aws.ToString(put.RevisionId), aws.ToString(del.RevisionId))

	_, err = client.GetResourcePolicy(ctx, &awsba.GetResourcePolicyInput{
		ResourceArn: aws.String("arn:aws:bedrock:us-east-1:123456789012:knowledge-base/MISSING000"),
	})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)
}

// TestMatchesResourcePolicyOnlyForKnowledgeBases keeps the shared
// /resourcepolicy root disjoint: bedrock-agent claims knowledge-base ARNs only,
// leaving VPC Lattice identifiers and path-style S3 keys to their handlers.
func TestMatchesResourcePolicyOnlyForKnowledgeBases(t *testing.T) {
	h := serverba.New(providerba.New(config.NewOptions()))

	cases := map[string]bool{
		"/resourcepolicy/arn:aws:bedrock:us-east-1:123456789012:knowledge-base/ABCDEFGHIJ": true,
		"/resourcepolicy/arn:aws:bedrock:us-east-1:123456789012:agent/ABCDEFGHIJ":          false,
		"/resourcepolicy/arn:aws:vpc-lattice:us-east-1:123456789012:service/svc-123":       false,
		"/resourcepolicy/svc-0123456789abcdef0":                                            false,
		"/resourcepolicy/my-object-key":                                                    false,
		"/flows/validate-definition":                                                       true,
	}

	for path, want := range cases {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		assert.Equal(t, want, h.Matches(r), path)
	}
}
