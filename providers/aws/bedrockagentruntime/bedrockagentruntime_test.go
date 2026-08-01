package bedrockagentruntime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("us-east-1"),
		config.WithAccountID("123456789012"),
	)

	return New(opts)
}

func TestInvokeAgent(t *testing.T) {
	m := newTestMock()

	out, err := m.InvokeAgent(context.Background(), driver.InvokeAgentInput{
		AgentID:      "AGENT123",
		AgentAliasID: "ALIAS123",
		SessionID:    "sess-1",
		InputText:    "hello agent",
	})
	if err != nil {
		t.Fatalf("InvokeAgent: %v", err)
	}

	if !strings.Contains(out.Completion, "hello agent") {
		t.Fatalf("completion %q does not echo the prompt", out.Completion)
	}

	if out.SessionID != "sess-1" {
		t.Fatalf("got session id %q, want sess-1", out.SessionID)
	}

	if out.ContentType != "application/json" {
		t.Fatalf("got content type %q, want application/json", out.ContentType)
	}
}

func TestInvokeAgentGeneratesSessionID(t *testing.T) {
	m := newTestMock()

	out, err := m.InvokeAgent(context.Background(), driver.InvokeAgentInput{
		AgentID:      "AGENT123",
		AgentAliasID: "ALIAS123",
	})
	if err != nil {
		t.Fatalf("InvokeAgent: %v", err)
	}

	if out.SessionID == "" {
		t.Fatal("expected a generated session id")
	}
}

func TestInvokeAgentValidation(t *testing.T) {
	m := newTestMock()

	if _, err := m.InvokeAgent(context.Background(), driver.InvokeAgentInput{AgentAliasID: "a"}); !errors.IsInvalidArgument(err) {
		t.Fatalf("want InvalidArgument for missing agentId, got %v", err)
	}

	if _, err := m.InvokeAgent(context.Background(), driver.InvokeAgentInput{AgentID: "a"}); !errors.IsInvalidArgument(err) {
		t.Fatalf("want InvalidArgument for missing agentAliasId, got %v", err)
	}
}

func TestRetrieve(t *testing.T) {
	m := newTestMock()

	out, err := m.Retrieve(context.Background(), driver.RetrieveInput{
		KnowledgeBaseID: "KB123",
		QueryText:       "what is bedrock",
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}

	if len(out.Results) == 0 {
		t.Fatal("expected non-empty retrieval results")
	}

	for _, r := range out.Results {
		if !strings.Contains(r.Text, "what is bedrock") {
			t.Fatalf("result %q does not echo the query", r.Text)
		}

		if r.LocationURI == "" {
			t.Fatal("expected a location uri")
		}
	}
}

func TestRetrieveValidation(t *testing.T) {
	m := newTestMock()

	if _, err := m.Retrieve(context.Background(), driver.RetrieveInput{KnowledgeBaseID: "KB"}); !errors.IsInvalidArgument(err) {
		t.Fatalf("want InvalidArgument for missing query text, got %v", err)
	}

	if _, err := m.Retrieve(context.Background(), driver.RetrieveInput{QueryText: "x"}); !errors.IsInvalidArgument(err) {
		t.Fatalf("want InvalidArgument for missing knowledgeBaseId, got %v", err)
	}
}

func TestRetrieveAndGenerate(t *testing.T) {
	m := newTestMock()

	out, err := m.RetrieveAndGenerate(context.Background(), driver.RetrieveAndGenerateInput{
		InputText: "summarize bedrock",
	})
	if err != nil {
		t.Fatalf("RetrieveAndGenerate: %v", err)
	}

	if !strings.Contains(out.Text, "summarize bedrock") {
		t.Fatalf("generated text %q does not echo the input", out.Text)
	}

	if out.SessionID == "" {
		t.Fatal("expected a generated session id")
	}
}

func TestRetrieveAndGenerateReusesSessionID(t *testing.T) {
	m := newTestMock()

	out, err := m.RetrieveAndGenerate(context.Background(), driver.RetrieveAndGenerateInput{
		InputText: "hi",
		SessionID: "existing-session",
	})
	if err != nil {
		t.Fatalf("RetrieveAndGenerate: %v", err)
	}

	if out.SessionID != "existing-session" {
		t.Fatalf("got session id %q, want existing-session", out.SessionID)
	}
}

func TestRetrieveAndGenerateValidation(t *testing.T) {
	m := newTestMock()

	if _, err := m.RetrieveAndGenerate(context.Background(), driver.RetrieveAndGenerateInput{}); !errors.IsInvalidArgument(err) {
		t.Fatalf("want InvalidArgument for missing input text, got %v", err)
	}
}
