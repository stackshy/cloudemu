package bedrockagent

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	provider "github.com/stackshy/cloudemu/v2/providers/aws/bedrockagent"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

func newService() *BedrockAgent {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return NewBedrockAgent(provider.New(opts))
}

func TestServiceAgentLifecycle(t *testing.T) {
	svc := newService()
	ctx := context.Background()

	agent, err := svc.CreateAgent(ctx, driver.AgentConfig{Name: "svc-agent", FoundationModel: "anthropic.claude-3-sonnet-20240229-v1:0"})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	if agent.ID == "" {
		t.Fatal("expected an agent id")
	}

	got, err := svc.GetAgent(ctx, agent.ID)
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}

	if got.Name != "svc-agent" {
		t.Fatalf("got name %q, want svc-agent", got.Name)
	}

	prepared, err := svc.PrepareAgent(ctx, agent.ID)
	if err != nil {
		t.Fatalf("PrepareAgent: %v", err)
	}

	if prepared.Status != driver.AgentPrepared {
		t.Fatalf("got status %q, want %q", prepared.Status, driver.AgentPrepared)
	}

	agents, _, err := svc.ListAgents(ctx, driver.Page{})
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}

	if len(agents) != 1 {
		t.Fatalf("got %d agents, want 1", len(agents))
	}

	if _, err := svc.DeleteAgent(ctx, agent.ID); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}

	if _, err := svc.GetAgent(ctx, agent.ID); err == nil {
		t.Fatal("expected GetAgent to fail after delete")
	}
}

func TestServiceKnowledgeBaseLifecycle(t *testing.T) {
	svc := newService()
	ctx := context.Background()

	kb, err := svc.CreateKnowledgeBase(ctx, driver.KnowledgeBaseConfig{
		Name:                       "svc-kb",
		RoleArn:                    "arn:aws:iam::123456789012:role/r",
		KnowledgeBaseConfiguration: []byte(`{"type":"VECTOR"}`),
		StorageConfiguration:       []byte(`{"type":"OPENSEARCH_SERVERLESS"}`),
	})
	if err != nil {
		t.Fatalf("CreateKnowledgeBase: %v", err)
	}

	got, err := svc.GetKnowledgeBase(ctx, kb.ID)
	if err != nil {
		t.Fatalf("GetKnowledgeBase: %v", err)
	}

	if got.Name != "svc-kb" {
		t.Fatalf("got name %q, want svc-kb", got.Name)
	}

	list, _, err := svc.ListKnowledgeBases(ctx, driver.Page{})
	if err != nil {
		t.Fatalf("ListKnowledgeBases: %v", err)
	}

	if len(list) != 1 {
		t.Fatalf("got %d knowledge bases, want 1", len(list))
	}
}

func TestServiceTagging(t *testing.T) {
	svc := newService()
	ctx := context.Background()

	prompt, err := svc.CreatePrompt(ctx, driver.PromptConfig{Name: "p", Tags: map[string]string{"a": "1"}})
	if err != nil {
		t.Fatalf("CreatePrompt: %v", err)
	}

	if err = svc.TagResource(ctx, prompt.ARN, map[string]string{"b": "2"}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	if err = svc.UntagResource(ctx, prompt.ARN, []string{"a"}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	tags, err := svc.ListTagsForResource(ctx, prompt.ARN)
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if len(tags) != 1 || tags["b"] != "2" {
		t.Fatalf("tags = %v, want map[b:2]", tags)
	}

	two := int32(2)

	for range 3 {
		if _, err = svc.CreateFlow(ctx, driver.FlowConfig{Name: "f", ExecutionRoleArn: "arn:aws:iam::123456789012:role/r"}); err != nil {
			t.Fatalf("CreateFlow: %v", err)
		}
	}

	flows, next, err := svc.ListFlows(ctx, driver.Page{MaxResults: &two})
	if err != nil || len(flows) != 2 || next == "" {
		t.Fatalf("ListFlows = %d items, next %q, err %v", len(flows), next, err)
	}
}
