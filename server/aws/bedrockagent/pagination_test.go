package bedrockagent_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsba "github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	batypes "github.com/aws/aws-sdk-go-v2/service/bedrockagent/types"
)

// TestSDKListAgentsPaginates covers maxResults and nextToken on a POST-bodied
// List op: two agents per page walks five agents in three pages.
func TestSDKListAgentsPaginates(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	for i := range 5 {
		if _, err := client.CreateAgent(ctx, &awsba.CreateAgentInput{
			AgentName: aws.String(fmt.Sprintf("agent-%d", i)),
		}); err != nil {
			t.Fatalf("CreateAgent: %v", err)
		}
	}

	first, err := client.ListAgents(ctx, &awsba.ListAgentsInput{MaxResults: aws.Int32(2)})
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}

	if len(first.AgentSummaries) != 2 || first.NextToken == nil {
		t.Fatalf("first page = %d items, nextToken %v; want 2 items and a token", len(first.AgentSummaries), first.NextToken)
	}

	seen := map[string]bool{}
	pages := 0

	p := awsba.NewListAgentsPaginator(client, &awsba.ListAgentsInput{MaxResults: aws.Int32(2)})
	for p.HasMorePages() {
		page, perr := p.NextPage(ctx)
		if perr != nil {
			t.Fatalf("NextPage: %v", perr)
		}

		pages++

		for _, s := range page.AgentSummaries {
			seen[aws.ToString(s.AgentId)] = true
		}
	}

	if pages != 3 || len(seen) != 5 {
		t.Fatalf("walked %d pages and %d distinct agents, want 3 and 5", pages, len(seen))
	}
}

// TestSDKListFlowsAndPromptsPaginate covers the query-string List ops.
func TestSDKListFlowsAndPromptsPaginate(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	for i := range 3 {
		if _, err := client.CreateFlow(ctx, &awsba.CreateFlowInput{
			Name:             aws.String(fmt.Sprintf("flow-%d", i)),
			ExecutionRoleArn: aws.String(roleArn),
		}); err != nil {
			t.Fatalf("CreateFlow: %v", err)
		}

		if _, err := client.CreatePrompt(ctx, &awsba.CreatePromptInput{
			Name: aws.String(fmt.Sprintf("prompt-%d", i)),
		}); err != nil {
			t.Fatalf("CreatePrompt: %v", err)
		}
	}

	flows, err := client.ListFlows(ctx, &awsba.ListFlowsInput{MaxResults: aws.Int32(2)})
	if err != nil {
		t.Fatalf("ListFlows: %v", err)
	}

	if len(flows.FlowSummaries) != 2 || flows.NextToken == nil {
		t.Fatalf("flows page = %d items, nextToken %v", len(flows.FlowSummaries), flows.NextToken)
	}

	rest, err := client.ListFlows(ctx, &awsba.ListFlowsInput{MaxResults: aws.Int32(2), NextToken: flows.NextToken})
	if err != nil {
		t.Fatalf("ListFlows page 2: %v", err)
	}

	if len(rest.FlowSummaries) != 1 || rest.NextToken != nil {
		t.Fatalf("flows page 2 = %d items, nextToken %v; want 1 and none", len(rest.FlowSummaries), rest.NextToken)
	}

	prompts, err := client.ListPrompts(ctx, &awsba.ListPromptsInput{MaxResults: aws.Int32(1)})
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}

	if len(prompts.PromptSummaries) != 1 || prompts.NextToken == nil {
		t.Fatalf("prompts page = %d items, nextToken %v", len(prompts.PromptSummaries), prompts.NextToken)
	}
}

// TestSDKListKnowledgeBasesAndDataSourcesPaginate covers the two remaining
// POST-bodied List ops.
func TestSDKListKnowledgeBasesAndDataSourcesPaginate(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	var kbID string

	for i := range 2 {
		kb, err := client.CreateKnowledgeBase(ctx, vectorKBInput(fmt.Sprintf("kb-%d", i), nil))
		if err != nil {
			t.Fatalf("CreateKnowledgeBase: %v", err)
		}

		kbID = aws.ToString(kb.KnowledgeBase.KnowledgeBaseId)
	}

	kbs, err := client.ListKnowledgeBases(ctx, &awsba.ListKnowledgeBasesInput{MaxResults: aws.Int32(1)})
	if err != nil {
		t.Fatalf("ListKnowledgeBases: %v", err)
	}

	if len(kbs.KnowledgeBaseSummaries) != 1 || kbs.NextToken == nil {
		t.Fatalf("kb page = %d items, nextToken %v", len(kbs.KnowledgeBaseSummaries), kbs.NextToken)
	}

	for i := range 3 {
		if _, err = client.CreateDataSource(ctx, s3DataSourceInput(kbID, fmt.Sprintf("ds-%d", i))); err != nil {
			t.Fatalf("CreateDataSource: %v", err)
		}
	}

	dss, err := client.ListDataSources(ctx, &awsba.ListDataSourcesInput{
		KnowledgeBaseId: aws.String(kbID),
		MaxResults:      aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("ListDataSources: %v", err)
	}

	if len(dss.DataSourceSummaries) != 2 || dss.NextToken == nil {
		t.Fatalf("ds page = %d items, nextToken %v", len(dss.DataSourceSummaries), dss.NextToken)
	}
}

func s3DataSourceInput(kbID, name string) *awsba.CreateDataSourceInput {
	return &awsba.CreateDataSourceInput{
		KnowledgeBaseId: aws.String(kbID),
		Name:            aws.String(name),
		DataSourceConfiguration: &batypes.DataSourceConfiguration{
			Type:            batypes.DataSourceTypeS3,
			S3Configuration: &batypes.S3DataSourceConfiguration{BucketArn: aws.String(bucketArn)},
		},
	}
}
