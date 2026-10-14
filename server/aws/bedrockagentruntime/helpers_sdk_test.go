package bedrockagentruntime_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsba "github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	batypes "github.com/aws/aws-sdk-go-v2/service/bedrockagent/types"
	agentruntime "github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

const (
	roleARN     = "arn:aws:iam::123456789012:role/bedrock"
	claudeModel = "anthropic.claude-3-sonnet-20240229-v1:0"
	instruction = "You are a support agent that answers questions about orders and refunds."
)

// stack is a full AWS server (as `cloudemu serve` builds it) with the clients
// the runtime tests use.
type stack struct {
	provider *awsprovider.Provider
	clock    *config.FakeClock
	runtime  *agentruntime.Client
	agent    *awsba.Client
	cw       *cloudwatch.Client
	url      string
}

func newStack(t *testing.T, opts ...config.Option) *stack {
	t.Helper()

	clock := config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	p := cloudemu.NewAWS(append([]config.Option{config.WithClock(clock)}, opts...)...)
	ts := httptest.NewServer(awsserver.NewFromProvider(p))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	require.NoError(t, err)

	return &stack{
		provider: p,
		clock:    clock,
		url:      ts.URL,
		runtime:  agentruntime.NewFromConfig(cfg, func(o *agentruntime.Options) { o.BaseEndpoint = aws.String(ts.URL) }),
		agent:    awsba.NewFromConfig(cfg, func(o *awsba.Options) { o.BaseEndpoint = aws.String(ts.URL) }),
		cw:       cloudwatch.NewFromConfig(cfg, func(o *cloudwatch.Options) { o.BaseEndpoint = aws.String(ts.URL) }),
	}
}

// newAgent creates an agent with the given action groups' executor and returns
// its id.
func (s *stack) newAgent(t *testing.T, name string, mutate func(*awsba.CreateAgentInput)) string {
	t.Helper()

	in := &awsba.CreateAgentInput{
		AgentName: aws.String(name), AgentResourceRoleArn: aws.String(roleARN),
		FoundationModel: aws.String(claudeModel), Instruction: aws.String(instruction),
	}
	if mutate != nil {
		mutate(in)
	}

	out, err := s.agent.CreateAgent(context.Background(), in)
	require.NoError(t, err)

	id := aws.ToString(out.Agent.AgentId)
	s.prepare(t, id)

	return id
}

// prepare prepares an agent's draft, as callers must before invoking it
// through TSTALIASID and again after every change to it.
func (s *stack) prepare(t *testing.T, agentID string) {
	t.Helper()

	_, err := s.agent.PrepareAgent(context.Background(), &awsba.PrepareAgentInput{AgentId: aws.String(agentID)})
	require.NoError(t, err)
}

// newKnowledgeBase creates a knowledge base with a CUSTOM data source holding
// the given documents (id -> text) and returns the two ids.
func (s *stack) newKnowledgeBase(t *testing.T, name string, docs map[string]string) (kbID, dsID string) {
	t.Helper()

	ctx := context.Background()

	kb, err := s.agent.CreateKnowledgeBase(ctx, &awsba.CreateKnowledgeBaseInput{
		Name: aws.String(name), RoleArn: aws.String(roleARN),
		KnowledgeBaseConfiguration: &batypes.KnowledgeBaseConfiguration{
			Type: batypes.KnowledgeBaseTypeVector,
			VectorKnowledgeBaseConfiguration: &batypes.VectorKnowledgeBaseConfiguration{
				EmbeddingModelArn: aws.String("arn:aws:bedrock:us-east-1::foundation-model/amazon.titan-embed-text-v1"),
			},
		},
		StorageConfiguration: &batypes.StorageConfiguration{
			Type: batypes.KnowledgeBaseStorageTypePinecone,
			PineconeConfiguration: &batypes.PineconeConfiguration{
				ConnectionString:     aws.String("https://example.pinecone.io"),
				CredentialsSecretArn: aws.String("arn:aws:secretsmanager:us-east-1:123456789012:secret:pc"),
				FieldMapping:         &batypes.PineconeFieldMapping{TextField: aws.String("t"), MetadataField: aws.String("m")},
			},
		},
	})
	require.NoError(t, err)

	kbID = aws.ToString(kb.KnowledgeBase.KnowledgeBaseId)

	ds, err := s.agent.CreateDataSource(ctx, &awsba.CreateDataSourceInput{
		KnowledgeBaseId: aws.String(kbID), Name: aws.String("custom"),
		DataSourceConfiguration: &batypes.DataSourceConfiguration{Type: batypes.DataSourceTypeCustom},
	})
	require.NoError(t, err)

	dsID = aws.ToString(ds.DataSource.DataSourceId)

	var items []batypes.KnowledgeBaseDocument
	for id, text := range docs {
		items = append(items, batypes.KnowledgeBaseDocument{Content: &batypes.DocumentContent{
			DataSourceType: batypes.ContentDataSourceTypeCustom,
			Custom: &batypes.CustomContent{
				CustomDocumentIdentifier: &batypes.CustomDocumentIdentifier{Id: aws.String(id)},
				SourceType:               batypes.CustomSourceTypeInLine,
				InlineContent: &batypes.InlineContent{
					Type: batypes.InlineContentTypeText, TextContent: &batypes.TextContentDoc{Data: aws.String(text)},
				},
			},
		}})
	}

	if len(items) > 0 {
		_, err = s.agent.IngestKnowledgeBaseDocuments(ctx, &awsba.IngestKnowledgeBaseDocumentsInput{
			KnowledgeBaseId: aws.String(kbID), DataSourceId: aws.String(dsID), Documents: items,
		})
		require.NoError(t, err)
	}

	return kbID, dsID
}

// requireAPIError asserts err is the named exception with the HTTP status.
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

// requireStreamDone fails on a stream error other than the transport teardown
// the SDK reader can report after the last event under load (see
// benignStreamTeardown). Callers assert the stream's content, so a truncated
// stream still fails.
func requireStreamDone(t *testing.T, err error) {
	t.Helper()

	if err != nil && !benignStreamTeardown(err) {
		require.NoError(t, err)
	}
}
