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

	agents, err := svc.ListAgents(ctx)
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

	list, err := svc.ListKnowledgeBases(ctx)
	if err != nil {
		t.Fatalf("ListKnowledgeBases: %v", err)
	}

	if len(list) != 1 {
		t.Fatalf("got %d knowledge bases, want 1", len(list))
	}
}
