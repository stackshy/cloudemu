package bedrockagent_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsba "github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	batypes "github.com/aws/aws-sdk-go-v2/service/bedrockagent/types"

	"github.com/stackshy/cloudemu/v2/config"
	providerba "github.com/stackshy/cloudemu/v2/providers/aws/bedrockagent"
	serverba "github.com/stackshy/cloudemu/v2/server/aws/bedrockagent"
)

const (
	roleArn     = "arn:aws:iam::123456789012:role/bedrock"
	embedArn    = "arn:aws:bedrock:us-east-1::foundation-model/amazon.titan-embed-text-v1"
	bucketArn   = "arn:aws:s3:::my-kb-bucket"
	claudeModel = "anthropic.claude-3-sonnet-20240229-v1:0"
)

func newClient(t *testing.T) *awsba.Client {
	t.Helper()

	drv := providerba.New(config.NewOptions())
	ts := httptest.NewServer(serverba.New(drv))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsba.NewFromConfig(cfg, func(o *awsba.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKAgentLifecycle(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	created, err := client.CreateAgent(ctx, &awsba.CreateAgentInput{
		AgentName:            aws.String("my-agent"),
		AgentResourceRoleArn: aws.String(roleArn),
		FoundationModel:      aws.String(claudeModel),
		Instruction:          aws.String("You are a helpful assistant that answers questions."),
	})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	agentID := aws.ToString(created.Agent.AgentId)
	if agentID == "" || aws.ToString(created.Agent.AgentArn) == "" {
		t.Fatalf("expected agent id + arn, got %+v", created.Agent)
	}

	if created.Agent.AgentStatus != batypes.AgentStatusNotPrepared {
		t.Fatalf("got status %q, want NOT_PREPARED", created.Agent.AgentStatus)
	}

	got, err := client.GetAgent(ctx, &awsba.GetAgentInput{AgentId: aws.String(agentID)})
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}

	if aws.ToString(got.Agent.AgentName) != "my-agent" {
		t.Fatalf("got name %q", aws.ToString(got.Agent.AgentName))
	}

	prep, err := client.PrepareAgent(ctx, &awsba.PrepareAgentInput{AgentId: aws.String(agentID)})
	if err != nil {
		t.Fatalf("PrepareAgent: %v", err)
	}

	if prep.AgentStatus != batypes.AgentStatusPrepared || prep.PreparedAt == nil {
		t.Fatalf("expected PREPARED with preparedAt, got %+v", prep)
	}

	list, err := client.ListAgents(ctx, &awsba.ListAgentsInput{})
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}

	if len(list.AgentSummaries) != 1 {
		t.Fatalf("got %d agent summaries, want 1", len(list.AgentSummaries))
	}

	upd, err := client.UpdateAgent(ctx, &awsba.UpdateAgentInput{
		AgentId:              aws.String(agentID),
		AgentName:            aws.String("my-agent-renamed"),
		AgentResourceRoleArn: aws.String(roleArn),
		FoundationModel:      aws.String(claudeModel),
	})
	if err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}

	if aws.ToString(upd.Agent.AgentName) != "my-agent-renamed" {
		t.Fatalf("update did not rename agent: %q", aws.ToString(upd.Agent.AgentName))
	}

	alias, err := client.CreateAgentAlias(ctx, &awsba.CreateAgentAliasInput{
		AgentId:        aws.String(agentID),
		AgentAliasName: aws.String("prod"),
	})
	if err != nil {
		t.Fatalf("CreateAgentAlias: %v", err)
	}

	if aws.ToString(alias.AgentAlias.AgentAliasId) == "" {
		t.Fatalf("expected alias id, got %+v", alias.AgentAlias)
	}

	if _, err = client.DeleteAgent(ctx, &awsba.DeleteAgentInput{AgentId: aws.String(agentID)}); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}

	_, err = client.GetAgent(ctx, &awsba.GetAgentInput{AgentId: aws.String(agentID)})
	assertNotFound(t, err)
}

func TestSDKAgentNotFound(t *testing.T) {
	client := newClient(t)

	_, err := client.GetAgent(context.Background(), &awsba.GetAgentInput{AgentId: aws.String("MISSING123")})
	assertNotFound(t, err)
}

func TestSDKKnowledgeBaseAndDataSourceLifecycle(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	kbCfg := &batypes.KnowledgeBaseConfiguration{
		Type: batypes.KnowledgeBaseTypeVector,
		VectorKnowledgeBaseConfiguration: &batypes.VectorKnowledgeBaseConfiguration{
			EmbeddingModelArn: aws.String(embedArn),
		},
	}

	kb, err := client.CreateKnowledgeBase(ctx, &awsba.CreateKnowledgeBaseInput{
		Name:                       aws.String("my-kb"),
		RoleArn:                    aws.String(roleArn),
		KnowledgeBaseConfiguration: kbCfg,
	})
	if err != nil {
		t.Fatalf("CreateKnowledgeBase: %v", err)
	}

	kbID := aws.ToString(kb.KnowledgeBase.KnowledgeBaseId)
	if kb.KnowledgeBase.Status != batypes.KnowledgeBaseStatusActive {
		t.Fatalf("got status %q, want ACTIVE", kb.KnowledgeBase.Status)
	}

	if kb.KnowledgeBase.KnowledgeBaseConfiguration == nil ||
		kb.KnowledgeBase.KnowledgeBaseConfiguration.Type != batypes.KnowledgeBaseTypeVector {
		t.Fatalf("knowledge base configuration did not round-trip: %+v", kb.KnowledgeBase.KnowledgeBaseConfiguration)
	}

	if _, err = client.GetKnowledgeBase(ctx, &awsba.GetKnowledgeBaseInput{
		KnowledgeBaseId: aws.String(kbID),
	}); err != nil {
		t.Fatalf("GetKnowledgeBase: %v", err)
	}

	kbList, err := client.ListKnowledgeBases(ctx, &awsba.ListKnowledgeBasesInput{})
	if err != nil {
		t.Fatalf("ListKnowledgeBases: %v", err)
	}

	if len(kbList.KnowledgeBaseSummaries) != 1 {
		t.Fatalf("got %d kb summaries, want 1", len(kbList.KnowledgeBaseSummaries))
	}

	testDataSourceLifecycle(t, client, kbID)

	if _, err = client.DeleteKnowledgeBase(ctx, &awsba.DeleteKnowledgeBaseInput{
		KnowledgeBaseId: aws.String(kbID),
	}); err != nil {
		t.Fatalf("DeleteKnowledgeBase: %v", err)
	}

	_, err = client.GetKnowledgeBase(ctx, &awsba.GetKnowledgeBaseInput{KnowledgeBaseId: aws.String(kbID)})
	assertNotFound(t, err)
}

func testDataSourceLifecycle(t *testing.T, client *awsba.Client, kbID string) {
	t.Helper()

	ctx := context.Background()
	dsCfg := &batypes.DataSourceConfiguration{
		Type:            batypes.DataSourceTypeS3,
		S3Configuration: &batypes.S3DataSourceConfiguration{BucketArn: aws.String(bucketArn)},
	}

	ds, err := client.CreateDataSource(ctx, &awsba.CreateDataSourceInput{
		KnowledgeBaseId:         aws.String(kbID),
		Name:                    aws.String("my-ds"),
		DataSourceConfiguration: dsCfg,
	})
	if err != nil {
		t.Fatalf("CreateDataSource: %v", err)
	}

	dsID := aws.ToString(ds.DataSource.DataSourceId)
	if ds.DataSource.Status != batypes.DataSourceStatusAvailable {
		t.Fatalf("got ds status %q, want AVAILABLE", ds.DataSource.Status)
	}

	got, err := client.GetDataSource(ctx, &awsba.GetDataSourceInput{
		KnowledgeBaseId: aws.String(kbID),
		DataSourceId:    aws.String(dsID),
	})
	if err != nil {
		t.Fatalf("GetDataSource: %v", err)
	}

	if got.DataSource.DataSourceConfiguration == nil ||
		got.DataSource.DataSourceConfiguration.Type != batypes.DataSourceTypeS3 {
		t.Fatalf("data source configuration did not round-trip: %+v", got.DataSource.DataSourceConfiguration)
	}

	job, err := client.StartIngestionJob(ctx, &awsba.StartIngestionJobInput{
		KnowledgeBaseId: aws.String(kbID),
		DataSourceId:    aws.String(dsID),
	})
	if err != nil {
		t.Fatalf("StartIngestionJob: %v", err)
	}

	if job.IngestionJob.Status != batypes.IngestionJobStatusComplete {
		t.Fatalf("got ingestion status %q, want COMPLETE", job.IngestionJob.Status)
	}

	if _, err = client.DeleteDataSource(ctx, &awsba.DeleteDataSourceInput{
		KnowledgeBaseId: aws.String(kbID),
		DataSourceId:    aws.String(dsID),
	}); err != nil {
		t.Fatalf("DeleteDataSource: %v", err)
	}
}

func TestSDKFlowLifecycle(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	created, err := client.CreateFlow(ctx, &awsba.CreateFlowInput{
		Name:             aws.String("my-flow"),
		ExecutionRoleArn: aws.String(roleArn),
		Description:      aws.String("a test flow"),
	})
	if err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}

	flowID := aws.ToString(created.Id)
	if aws.ToString(created.Arn) == "" || created.Status != batypes.FlowStatusNotPrepared {
		t.Fatalf("expected arn + NotPrepared, got %+v", created)
	}

	got, err := client.GetFlow(ctx, &awsba.GetFlowInput{FlowIdentifier: aws.String(flowID)})
	if err != nil {
		t.Fatalf("GetFlow: %v", err)
	}

	if aws.ToString(got.Name) != "my-flow" {
		t.Fatalf("got flow name %q", aws.ToString(got.Name))
	}

	prep, err := client.PrepareFlow(ctx, &awsba.PrepareFlowInput{FlowIdentifier: aws.String(flowID)})
	if err != nil {
		t.Fatalf("PrepareFlow: %v", err)
	}

	if prep.Status != batypes.FlowStatusPrepared {
		t.Fatalf("got flow status %q, want Prepared", prep.Status)
	}

	list, err := client.ListFlows(ctx, &awsba.ListFlowsInput{})
	if err != nil {
		t.Fatalf("ListFlows: %v", err)
	}

	if len(list.FlowSummaries) != 1 {
		t.Fatalf("got %d flow summaries, want 1", len(list.FlowSummaries))
	}

	if _, err = client.DeleteFlow(ctx, &awsba.DeleteFlowInput{FlowIdentifier: aws.String(flowID)}); err != nil {
		t.Fatalf("DeleteFlow: %v", err)
	}

	_, err = client.GetFlow(ctx, &awsba.GetFlowInput{FlowIdentifier: aws.String(flowID)})
	assertNotFound(t, err)
}

func TestSDKPromptLifecycle(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	created, err := client.CreatePrompt(ctx, &awsba.CreatePromptInput{
		Name:        aws.String("my-prompt"),
		Description: aws.String("a test prompt"),
	})
	if err != nil {
		t.Fatalf("CreatePrompt: %v", err)
	}

	promptID := aws.ToString(created.Id)
	if aws.ToString(created.Arn) == "" || aws.ToString(created.Version) != "DRAFT" {
		t.Fatalf("expected arn + DRAFT version, got %+v", created)
	}

	got, err := client.GetPrompt(ctx, &awsba.GetPromptInput{PromptIdentifier: aws.String(promptID)})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}

	if aws.ToString(got.Name) != "my-prompt" {
		t.Fatalf("got prompt name %q", aws.ToString(got.Name))
	}

	list, err := client.ListPrompts(ctx, &awsba.ListPromptsInput{})
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}

	if len(list.PromptSummaries) != 1 {
		t.Fatalf("got %d prompt summaries, want 1", len(list.PromptSummaries))
	}

	if _, err = client.DeletePrompt(ctx, &awsba.DeletePromptInput{PromptIdentifier: aws.String(promptID)}); err != nil {
		t.Fatalf("DeletePrompt: %v", err)
	}

	_, err = client.GetPrompt(ctx, &awsba.GetPromptInput{PromptIdentifier: aws.String(promptID)})
	assertNotFound(t, err)
}

// TestMatchesAnchorsPrefixes guards the M2 fix: bucket-style paths that merely
// share a prefix (e.g. "/flows-prod") must NOT be claimed by the Bedrock Agent
// handler, so they fall through to the S3 catch-all, while the documented
// collection/item shapes still match.
func TestMatchesAnchorsPrefixes(t *testing.T) {
	h := serverba.New(providerba.New(config.NewOptions()))

	cases := []struct {
		path string
		want bool
	}{
		// Bucket-style paths must fall through to S3.
		{"/flows-prod", false},
		{"/promptsdb", false},
		{"/knowledgebases-archive", false},
		{"/agents-backup", false},
		// Documented collection and item shapes must still be claimed.
		{"/agents/", true},
		{"/knowledgebases", true},
		{"/knowledgebases/kb-123", true},
		{"/knowledgebases/kb-123/datasources/ds-1", true},
		{"/flows/", true},
		{"/flows/flow-1/", true},
		{"/prompts/", true},
		{"/prompts/prompt-1/", true},
	}

	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		if got := h.Matches(req); got != tc.want {
			t.Errorf("Matches(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func assertNotFound(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("expected ResourceNotFoundException, got nil")
	}

	var nfe *batypes.ResourceNotFoundException
	if !errors.As(err, &nfe) {
		t.Fatalf("expected ResourceNotFoundException, got %T: %v", err, err)
	}
}
