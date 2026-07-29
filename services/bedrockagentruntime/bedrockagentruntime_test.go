package bedrockagentruntime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	provider "github.com/stackshy/cloudemu/v2/providers/aws/bedrockagentruntime"
	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

func newService() *BedrockAgentRuntime {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return NewBedrockAgentRuntime(provider.New(opts))
}

func TestServiceInvokeAgent(t *testing.T) {
	svc := newService()

	out, err := svc.InvokeAgent(context.Background(), driver.InvokeAgentInput{
		AgentID:      "AGENT1",
		AgentAliasID: "ALIAS1",
		SessionID:    "s1",
		InputText:    "hi",
	})
	if err != nil {
		t.Fatalf("InvokeAgent: %v", err)
	}

	if !strings.Contains(out.Completion, "hi") || out.SessionID != "s1" {
		t.Fatalf("unexpected result: %+v", out)
	}
}

func TestServiceRetrieve(t *testing.T) {
	svc := newService()

	out, err := svc.Retrieve(context.Background(), driver.RetrieveInput{
		KnowledgeBaseID: "kb1",
		QueryText:       "q",
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}

	if len(out.Results) == 0 {
		t.Fatal("expected results")
	}
}

func TestServiceRetrieveAndGenerate(t *testing.T) {
	svc := newService()

	out, err := svc.RetrieveAndGenerate(context.Background(), driver.RetrieveAndGenerateInput{InputText: "q"})
	if err != nil {
		t.Fatalf("RetrieveAndGenerate: %v", err)
	}

	if out.Text == "" || out.SessionID == "" {
		t.Fatalf("unexpected result: %+v", out)
	}
}
